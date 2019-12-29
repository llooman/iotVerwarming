package verwarming

// TODO Mvc ??
// TODO use SendProp for ketel.Schakelaar   +  download.
// TODO what if thuisKnop on and after 22:00
// TODO messure the real warmup speed
// TODO minimum energy in the wall.

/*
  < kamer 20 graden dan 100% verwarmen.
  > kamer 22 graden dan verwarming uit.

  vervroegen met factor: factor bijstellen ,5 graden per uur

  Tussen 20-22 graden regelmatig energie in de muur stoppen

  calcDuty: evaluate the current tempature and calculates how on/off counters.
  checkAgenda:  set agenda array for today so we can check if we are home or away according the agenda.
  updateTimers: keep track of warming up and cooling down time in seconds. debounce timer
  loop:         calc heating capacity needed for the current room tempature.
                calc the on/off periods base on the capacity
				check periodes and swith heating on/off.
*/

import (
	"fmt"
	"iot"
	"os"
	"strconv"
	"strings"
	"time"
)

var initialized bool
var verwLoopRunning bool
var nextActivity int64
var prevActivity int64

var node *iot.IotNode
var NodeId int // initialized when > 0

var Actief *iot.IotProp
var Power *iot.IotProp
var DutyOn *iot.IotProp
var DutyOff *iot.IotProp

var ThuisKnop *iot.IotProp
var WegKnop *iot.IotProp

var prevPompTemp int64
var opwarmTimer *iot.IotProp // aantal seconds temp muur in >= 40, reset when start counting
var afkoelTimer *iot.IotProp // aantal seconden temp muur in <= 38, reset when start counting

var OpwarmSpeed *iot.IotProp

// int     maxOffPeriode = 1800;  // max 30 min off
// long 	debounceTimestamp = 0;
// long 	prevTempTimestamp = 0;
// long 	prevTemp = 0;
// long 	debounceLimit 	= 180000; //ms,  debounce for 3 minute

var defaultVroeger int // in minutes

var kamerTempMin float32
var kamerTempMax float32

var ketelSchakelaar bool
var debouncePeriode int // seconds
var debounceTimer int

var periodTest string   // test
var kamerTempTest int64 // test

var kamerTemp float32
var pompTemp float32

func init() {
	initialized = false
	NodeId = 0
	defaultVroeger = 30
	kamerTempMax = 22.0
	kamerTempMin = 19.5

	debouncePeriode = 60 // sec
	debounceTimer = 0

	// ketelAan = false

	// var ketelAan bool
	// var verwarmen int 			// aantal seconds temp muur in >= 40, reset when start counting
	// var nietVerwarmen int 		// aantal seconden temp muur in <= 38, reset when start counting
}

func Setup(verw *iot.IotNode) {

	node = verw
	if !verw.IsInit {
		verw.Name = "Verwarm"
		verw.ConnId = 2 // no connector needed
		// verw.DefDirty = false
		verw.IsInit = true
		iot.PersistNodeDef(verw)
	}

	Actief = iot.GetOrDefaultProp(verw, 10, "actief", 0, 3600, 0, 0, true)
	Power = iot.GetOrDefaultProp(verw, 11, "power", 0, 3600, 0, 0, true)
	DutyOn = iot.GetOrDefaultProp(verw, 12, "dutyOn", 0, 3600, 0, 0, true)
	DutyOff = iot.GetOrDefaultProp(verw, 13, "dutyOff", 0, 3600, 0, 0, true)
	ThuisKnop = iot.GetOrDefaultProp(verw, 20, "thuis", 0, 3600, 0, 0, true)
	WegKnop = iot.GetOrDefaultProp(verw, 21, "weg", 0, 3600, 0, 0, true)

	OpwarmSpeed = iot.GetOrDefaultProp(verw, 22, "speed", 0, 3600, 0, 0, true)
	if OpwarmSpeed.Val == 0 {
		OpwarmSpeed.Val = 50
	}
	opwarmTimer = iot.GetOrDefaultProp(verw, 30, "opwarmTimer", 0, 3600, 0, 0, true)
	afkoelTimer = iot.GetOrDefaultProp(verw, 31, "afkoelTimer", 0, 3600, 0, 0, true)

	NodeId = verw.NodeId

	// ThuisKnop.Val = 0
	// periodTest = "11:55-22:00"
	// kamerTempTest = 2000

	//fmt.Printf("Setup:%s\n ", AppName)
}

func checkInitialized() bool {

	// fmt.Printf("checkInitialized:\n ")

	if iot.KamerTemp.IsInit &&
		iot.KetelSchakelaar.IsInit &&
		iot.PompTemp.IsInit {
		return true
	}

	return false
}

func Loop() {
	verwLoopRunning = true
	nextActivity = 0
	prevActivity = time.Now().Unix()

	iot.Info.Printf("Loop:%s Start\n", node.Name)

	for verwLoopRunning {

		if time.Now().Unix() >= nextActivity {

			nextActivity = time.Now().Unix() + 2

			if !initialized {
				if initialized = checkInitialized(); !initialized {
					nextActivity = time.Now().Unix() + 1
					continue
				}
			}

			kamerTemp = float32(iot.KamerTemp.Val) / 100.0
			pompTemp = float32(iot.PompTemp.Val) / 100.0

			// fmt.Printf("------- verwarming -------\n")
			// fmt.Printf("kamperTemp:%f, ketelSchakelaar:%t, pompTemp:%d\n", kamerTemp, iot.KetelSchakelaar.Val > 0, iot.PompTemp.Val)

			updateTimers()
			resetThuisKnop()
			checkAgenda()
			calcDuty()

			if debounceTimer < debouncePeriode {
				iot.Trace.Printf("verwarming debounce:%d Ketel:%t pomp:%f kamer:%f opwarm:%d, afkoel:%d \n", debounceTimer, iot.KetelSchakelaar.Val > 0, pompTemp, kamerTemp, opwarmTimer.Val, afkoelTimer.Val)
				continue
			}

			if Actief.Val <= 0 ||
				(DutyOn.Val <= 0 &&
					DutyOff.Val <= 0) {

				if iot.KetelSchakelaar.Val > 0 {
					iot.SetProp(iot.KetelSchakelaar, 0, 0)
					debounceTimer = 0
					if iot.LogCode("v") {
						iot.Info.Printf("Verwarming OFF \n")
					}
				}

			} else {

				if DutyOn.Val <= 0 {
					if iot.KetelSchakelaar.Val > 0 {
						iot.SetProp(iot.KetelSchakelaar, 0, 0)
						debounceTimer = 0
						if iot.LogCode("v") {
							iot.Info.Printf("DutyOn ZERO\n")
						}
					}

				} else if DutyOff.Val <= 0 {

					if iot.KetelSchakelaar.Val <= 0 {
						iot.SetProp(iot.KetelSchakelaar, 1, 0)
						debounceTimer = 0
						if iot.LogCode("v") {
							iot.Info.Printf("DutyOff ZERO\n")
						}
					}

				} else { // modelation modes

					if iot.KetelSchakelaar.Val > 0 &&
						opwarmTimer.Val > DutyOn.Val {

						iot.SetProp(iot.KetelSchakelaar, 0, 0)
						iot.SaveProp(afkoelTimer, 0, 0)
						debounceTimer = 0
						if iot.LogCode("v") {
							iot.Info.Printf("DutyOn finished \n")
						}

					} else if iot.KetelSchakelaar.Val <= 0 &&
						afkoelTimer.Val > DutyOff.Val {

						iot.SetProp(iot.KetelSchakelaar, 1, 0)
						iot.SaveProp(opwarmTimer, 0, 0)
						debounceTimer = 0
						if iot.LogCode("v") {
							iot.Info.Printf("DutyOff finished\n")
						}
					}
				}
			}

			if Actief.Val <= 0 {
				if iot.LogCode("v") {
					iot.Trace.Printf("Verwarming OFF\n")
				}

			} else if DutyOn.Val <= 0 {
				if iot.LogCode("v") {
					iot.Trace.Printf("DutyOn ZERO\n")
				}

			} else {
				if iot.LogCode("v") {
					iot.Info.Printf("Power:%d dutyOn:%d dutyOff:%d Ketel:%t pmpTemp:%f kamer:%f opwarm:%d, afkoel:%d\n", Power.Val, DutyOn.Val, DutyOff.Val, iot.KetelSchakelaar.Val > 0, pompTemp, kamerTemp, opwarmTimer.Val, afkoelTimer.Val)
				}
			}
		}

		time.Sleep(999 * time.Millisecond)
	}

	iot.Err.Printf("Verwarming loop Finished ???\n ")
}

func updateTimers() {

	if prevPompTemp >= 4000 &&
		iot.PompTemp.Val >= 4000 {

		iot.SaveProp(opwarmTimer, opwarmTimer.Val+(time.Now().Unix()-prevActivity), 0)

	} else if true &&
		prevPompTemp <= 3800 &&
		iot.PompTemp.Val <= 3800 {

		iot.SaveProp(afkoelTimer, afkoelTimer.Val+(time.Now().Unix()-prevActivity), 0)
	}

	debounceTimer = debounceTimer + int(time.Now().Unix()-prevActivity)
	prevPompTemp = iot.PompTemp.Val
	prevActivity = time.Now().Unix()

}

func Command(iotPayload *iot.IotPayload) string {

	iot.Trace.Printf("verwarmingCommand: %v\n", iotPayload)

	switch iotPayload.Cmd {

	case "S":

		prop := iot.GetProp(iotPayload.NodeId, iotPayload.PropId) // test exist ???
		payloadVal, err := strconv.ParseInt(iotPayload.Val, 10, 32)

		if err != nil {
			iot.Trace.Printf("Verwarming.Command val not number! %v", iotPayload)
			return fmt.Sprintf(`{"retcode":99,"message":"cmd %s needs number val!!"}`, iotPayload.Cmd)
		}

		// toggle thuis
		if iotPayload.PropId == 20 {

			if payloadVal > prop.Val {
				// aan
				iot.SetProp(WegKnop, 0, 0)
				// WegKnop.Val = 0
				iot.MvcUp(WegKnop)
				nextActivity = 0

			} else if payloadVal < prop.Val {
				// uit
				nextActivity = 0

			}

			// toggle weg
		} else if iotPayload.PropId == 21 {

			if payloadVal > prop.Val {
				// aan
				iot.SetProp(ThuisKnop, 0, 0)
				// ThuisKnop.Val = 0
				iot.MvcUp(ThuisKnop)
				nextActivity = 0

			} else if payloadVal < prop.Val {
				// uit
				nextActivity = 0

			}

		}

		if prop.Val == payloadVal &&
			prop.ValStamp == iotPayload.Timestamp &&
			iotPayload.Timestamp > 0 {

			iot.Trace.Printf("localCommand:skip equal update\n")
			return fmt.Sprintf(`{"retcode":0,"message":"varId %d duplicate Set"}`, iotPayload.VarId)
		}

		if prop.ValStamp > iotPayload.Timestamp &&
			iotPayload.Timestamp > 0 {

			iot.Trace.Printf("localCommand:skip new timestamp to old!\n")
			return fmt.Sprintf(`{"retcode":0,"message":"varId %d skip Set aged Set"}`, iotPayload.VarId)
		}

		prop.IsDataSaved = false
		// prop.IsNew = false
		if prop.Decimals >= 0 {
			prop.Val = payloadVal
			if iotPayload.Timestamp > 0 {
				prop.ValStamp = iotPayload.Timestamp
			} else {
				prop.ValStamp = time.Now().Unix()
			}

		} else {
			//	prop.ValString
		}

	case "R":
		return fmt.Sprintf(`{"retcode":0,"message":"cmd %s no need for localCommand"}`, iotPayload.Cmd)

	default:
		return fmt.Sprintf(`{"retcode":99,"message":"cmd %s not found"}`, iotPayload.Cmd)
	}

	return fmt.Sprintf(`{"retcode":0,"message":"cmd %s"}`, iotPayload.Cmd)
}

func Stop() {
	verwLoopRunning = false
}

func Now() {
	verwLoopRunning = true
	nextActivity = 0
}

/*
 * calc remaining part of dutycycle given periode and perc
 * = periode * (100 - perc) /perc
 */
func calcRemainingPeriode(period int, perc int) int {
	return (period * (100 - perc)) / perc
}

/*
 * calculate heating capacity based on the room tempature
 * first set power
 * then calc dutycycle in seconds on and off.
 */
func calcDuty() {

	var power int
	var dutyOn int
	var dutyOff int

	kamerTemp := float32(iot.KamerTemp.Val) / 100.0

	if kamerTempTest > 0 {
		kamerTemp = float32(kamerTempTest) / 100.0
	}

	if Actief.Val <= 0 ||
		kamerTemp > 22.0 {
		power = 0

	} else if kamerTemp <= 19.0 {
		power = 100

	} else {
		power = int(100.0 * ((kamerTempMax - kamerTemp) / (kamerTempMax - kamerTempMin)))
	}

	if power < 5 {
		dutyOff = 100
		dutyOn = 0

	} else if power > 90 {
		dutyOff = 0
		dutyOn = 100

		/* calculate duty cycle
		 *
		 * When capacity neede > 50% then we choose:
		 * fixed OFF-period and then calcRemainingPeriode the ON-period
		 *                  else
		 * fixed ON-period and then calcRemainingPeriode the OFF-period
		 */

	} else if power > 70 {
		dutyOff = 120 // 2 minuten
		dutyOn = calcRemainingPeriode(dutyOff, power)

	} else if power > 50 {
		dutyOff = 360 // 6 minuten
		dutyOn = calcRemainingPeriode(dutyOff, power)

	} else if power > 20 {
		dutyOn = 360 // 6 minuten
		dutyOff = calcRemainingPeriode(dutyOn, power)
		dutyOn = dutyOn + 60

	} else {
		dutyOn = 120 // 2 minuten
		dutyOff = calcRemainingPeriode(dutyOn, power)
		dutyOn = dutyOn + 60
	}

	//fmt.Printf("power:%d dutyOn:%d dutyOff:%d \n", power, dutyOn, dutyOff)

	iot.SetProp(Power, int64(power), 0)
	iot.SetProp(DutyOn, int64(dutyOn), 0)
	iot.SetProp(DutyOff, int64(dutyOff), 0)

	// 	float deltaTemp = (tempHoog - tempLaag )/100f ;
	// 	float duration = (timestampHoog-timestampLaag)/(3600000);
	// 	float verwSpeed = -1;

	// 	//                         graden per uur.
	// 	if(duration>0) verwSpeed =  deltaTemp/duration;

}

/*

minutesOfDay: 1333 nu:22:13 vandaag:11-13 kamperTemp:2200

*  type 1 = yyyy-mm-dd    specifieke dag in een jaar b.v. hemelvaart elk jaar andere datum
*       5 = mm-d        jaarlijkse datum   b.v. kerst

    !!!Mmm-L        laatste dag van de maand.

*       9 = weekday

	type 3: di 07:00-09:00,15:00-22:00
	type 2: 01-1 09:00-22:00
	type 1: 20200208 09:00-22:00
	type 1: 20200208 null = weg

*       :) find out feest datum isHemelvaart
* */

func checkAgenda() {

	active := false
	vroeger := 0
	kamerTemp := iot.KamerTemp.Val

	if kamerTempTest > 0 {
		kamerTemp = kamerTempTest
	}

	// if kamerTemp == 0 {
	// 	return
	// }

	if kamerTemp <= 2000 {
		vroeger = 60 * (2000 - int(kamerTemp)) / 50 // opwarm snelheird = 0.5 graden per uur
		if vroeger < defaultVroeger {
			vroeger = defaultVroeger
		}
	}

	minutesOfDay := time.Now().Hour()*60 + time.Now().Minute()
	jjjjmmdd := fmt.Sprintf("%d-%02d-%02d", time.Now().Year(), time.Now().Month(), time.Now().Day())
	mmdd := fmt.Sprintf("%02d-%d", time.Now().Month(), time.Now().Day())
	weekDay := fmt.Sprintf("%d", time.Now().Weekday())
	//fmt.Printf("checkAgenda minutesOfDay:%d jjjjmmdd:%s mmdd:%s weekDay:%s\n", minutesOfDay, jjjjmmdd, mmdd, weekDay)

	sql := fmt.Sprintf(`Select perioden from verwarming where ( datum = '%s' or datum = '%s' or datum = '%s') order by type asc`, jjjjmmdd, mmdd, weekDay)
	//fmt.Printf("sql:%s\n", sql)

	rows, err := iot.DatabaseNew.Query(sql)
	checkError2(err)

	var perioden string
	defer rows.Close()
	rows.Next()
	err = rows.Scan(&perioden)
	checkError2(err)

	if len(periodTest) > 0 {
		perioden = periodTest
	}

	if perioden == "" {
		active = false
	} else {

		//ex: 07:00-09:30,15:00-22:00
		dayParts := strings.Split(perioden, ",")

		for _, dayPart := range dayParts {

			//ex: 07:00-09:30
			vanTot := strings.Split(dayPart, "-")
			vanHHMM := strings.Split(vanTot[0], ":")
			vanHH, _ := strconv.Atoi(vanHHMM[0])
			vanMM, _ := strconv.Atoi(vanHHMM[1])
			van := vanHH*60 + vanMM
			totHHMM := strings.Split(vanTot[1], ":")
			totHH, _ := strconv.Atoi(totHHMM[0])
			totMM, _ := strconv.Atoi(totHHMM[1])
			tot := totHH*60 + totMM

			//fmt.Printf("check:  minutesOfDay:%d vroeger:%d van:%d tot:%d \n", minutesOfDay, vroeger, van, tot)

			if minutesOfDay >= (van-vroeger) &&
				minutesOfDay <= tot {
				active = true
				break
			}
		}
	}

	if ThuisKnop.Val > 0 {
		active = true
	} else if WegKnop.Val > 0 {
		active = false
	}

	if active {
		// Actief.Val = 1
		iot.SetProp(Actief, 1, 0)
	} else {
		iot.SetProp(Actief, 0, 0)
		// Actief.Val = 0
	}

	// fmt.Printf("checkAgenda:%s kamerTemp:%d vroeger:%d Actief:%t\n", perioden, kamerTemp, vroeger, Actief.Val > 0)
}

func checkError2(err error) {
	if err != nil {
		iot.Err.Printf("err: " + err.Error())
		fmt.Fprintf(os.Stderr, "iot Fatal error: %s", err.Error())
		os.Exit(1)
	}
}

func resetThuisKnop() {

	if ThuisKnop.Val > 0 &&
		time.Now().Hour() == 22 &&
		time.Now().Minute() < 5 {

		// ThuisKnop.Val = 0
		// iot.SaveProp(ThuisKnop, 0, time.Now().Unix(), 0)
		iot.SetProp(ThuisKnop, 0, 0)
		// iot.MvcUp(ThuisKnop)
		nextActivity = 0
	}
}
