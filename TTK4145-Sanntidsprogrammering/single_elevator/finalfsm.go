package single_elevator

import (
	"TTK4145-Heislab/configuration"
	"TTK4145-Heislab/driver-go/elevio"
	"fmt"
	"time"
)

//Are states well updated in FSM?

// MÅ ENDRE NAVN FRA STATE TIL ELEVATOR: STATE ER MISVISENDE
type State struct { //the elevators current state
	Floor       int
	Direction   Direction //directions: Up, Down
	Obstructed  bool
	Behaviour   Behaviour //behaviours: Idle, Moving and DoorOpen
	Unavailable bool      //MÅ OPPDATERE
}

type Behaviour int

const (
	Idle Behaviour = iota
	Moving
	DoorOpen //completing current order at requested floor
)

// can print out behaviour of elevator
func ToString(behaviour Behaviour) string {
	switch behaviour {
	case Idle:
		return "Idle"
	case Moving:
		return "Moving"
	case DoorOpen:
		return "DoorOpen"
	default:
		return "Unknown"
	}
}

func runTimer(duration time.Duration, timeOutChannel chan<- bool, resetTimerChannel <-chan bool) {
	deadline := time.Now().Add(100000 * time.Hour)
	is_running := false

	for {
		select {
		case <-resetTimerChannel:
			deadline = time.Now().Add(duration)
			is_running = true
		default:
			if is_running && time.Since(deadline) > 0 {
				timeOutChannel <- true
				is_running = false
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func findClosestFloor() int {
	for {
		floor := elevio.GetFloor()
		if floor != -1 {
			return floor
		}
		time.Sleep(100 * time.Millisecond) // Sjekker hvert 100 ms
	}
}

//WHERE TO UPDATE FLOOR - updating on channel at all times.

func SingleElevator(
	newOrderChannel <-chan Orders, //receiving new orders FROM ORDER MANAGER
	completedOrderChannel chan<- elevio.ButtonEvent, //sending information about completed orders TO ORDER MANAGER
	newLocalStateChannel chan<- State, //sending information about the elevators current state TO ORDER MANAGER

) {
	//Initialization of elevator
	fmt.Println("Initializing elevator...")

	var state State
	currentFloor := elevio.GetFloor()

	if currentFloor != -1 {
		fmt.Println("Heis starter i etasje", currentFloor)
		state = State{Floor: currentFloor, Behaviour: Idle, Direction: elevio.MD_Stop}
	} else {
		fmt.Println("Heis er mellom to etasjer, søker nærmeste...")
		closestFloor := findClosestFloor()
		if closestFloor > state.Floor {
			fmt.Println("Beveger opp til nærmeste etasje:", closestFloor)
			elevio.SetMotorDirection(elevio.MD_Up)
			state = State{Behaviour: Moving, Direction: Direction(elevio.MD_Up)}
		} else {
			fmt.Println("Beveger ned til nærmeste etasje:", closestFloor)
			elevio.SetMotorDirection(elevio.MD_Down)
			state = State{Behaviour: Moving, Direction: elevio.MD_Down}
		}
	}

	/*
		fmt.Println("setting motor down")
		elevio.SetMotorDirection(elevio.MD_Down)
		state := State{Direction: Down, Behaviour: Moving}
	*/

	//creating channels for communication
	floorEnteredChannel := make(chan int) //tells which floor elevator is at
	obstructedChannel := make(chan bool, 16)
	stopPressedChannel := make(chan bool, 16)

	//starting go-routines for foor and floorsensor
	go elevio.PollFloorSensor(floorEnteredChannel)

	timerOutChannel := make(chan bool)
	resetTimerChannel := make(chan bool)
	go runTimer(configuration.DoorOpenDuration, timerOutChannel, resetTimerChannel)
	//go startTimer(configuration.DoorOpenDuration, timerOutChannel)

	go elevio.PollObstructionSwitch(obstructedChannel)
	go elevio.PollStopButton(stopPressedChannel)

	var OrderMatrix Orders //matrix for orders

	for {
		select {
		case <-timerOutChannel: //timeroutchannel - må sende en verdi til den noe sted!!
			switch state.Behaviour {
			case DoorOpen:
				DirectionBehaviourPair := ordersChooseDirection(state.Floor, state.Direction, OrderMatrix)
				state.Behaviour = DirectionBehaviourPair.Behaviour
				state.Direction = Direction(DirectionBehaviourPair.Direction)
				newLocalStateChannel <- state
				switch state.Behaviour {
				case DoorOpen:
					//start timer på nytt og rydd forespørsler i nåværende etasje
					resetTimerChannel <- true
					OrderCompletedatCurrentFloor(state.Floor, Direction(state.Direction.convertMD()), completedOrderChannel) //requests cleared
					newLocalStateChannel <- state
				case Moving, Idle:
					elevio.SetDoorOpenLamp(false)
					elevio.SetMotorDirection(DirectionBehaviourPair.Direction)

				}
			case Moving:
				panic("timeroutchannel in moving")
			}
		case stopbuttonpressed := <-stopPressedChannel:

			if stopbuttonpressed {
				fmt.Println("StopButton is pressed")
				elevio.SetStopLamp(true)
				elevio.SetMotorDirection(elevio.MD_Stop)
			} else {
				elevio.SetStopLamp(false)
			}
			//hva må man gjøre. Skrive reset time channel case for obstruction
			//for løkke//finne ut oppdatering på obstruction, skjer kun når ting endrer seg

		case obstruction := <-obstructedChannel:
			if obstruction {
				state.Obstructed = true
				state.Unavailable = true
				fmt.Println("Obstruction detected! Elevator unavailable")
				state.Behaviour = DoorOpen
				elevio.SetDoorOpenLamp(true)
				newLocalStateChannel <- state
				resetTimerChannel <- true

				// Hold døren aktiv mens obstruction er aktiv
				for obstruction {
					select {
					case obstruction = <-obstructedChannel:
						if !obstruction {
							state.Obstructed = false
							state.Unavailable = false
							fmt.Println("Obstruction cleared! Elevator available.")
							newLocalStateChannel <- state
							if state.Behaviour == DoorOpen {
								resetTimerChannel <- true
							}
						}
					default:
						if state.Behaviour == DoorOpen {
							resetTimerChannel <- true
						}
					}
				}
			}
			/*
				switch state.Behaviour {
				case DoorOpen:
					resetTimerChannel <- true
					fmt.Println("Obstruction switch ON")
					newLocalStateChannel <- state //NEW LOCAL STATE MÅ OPPDATERES OVERALT
				case Moving, Idle:
					continue
				}
			*/

		case state.Floor = <-floorEnteredChannel: //if order at current floor
			fmt.Println("New floor: ", state.Floor)
			elevio.SetFloorIndicator(state.Floor)
			//sjekker om vi har bestillinger i state.Floor, if yes. stop. and clear floor orders
			switch state.Behaviour {
			case Moving:
				if orderHere(OrderMatrix, state.Floor) || state.Floor == 0 || state.Floor == configuration.NumFloors-1 {
					elevio.SetMotorDirection(elevio.MD_Stop)
					OrderCompletedatCurrentFloor(state.Floor, Direction(state.Direction.convertMD()), completedOrderChannel) //requests cleared
					resetTimerChannel <- true
					state.Behaviour = DoorOpen
					newLocalStateChannel <- state
					fmt.Println("New local state:", state)
				}
			default:
			}
		case OrderMatrix = <-newOrderChannel:
			fmt.Println("New orders :)")
			switch state.Behaviour {
			case Idle:
				state.Behaviour = Moving
				DirectionBehaviourPair := ordersChooseDirection(state.Floor, state.Direction, OrderMatrix)
				state.Behaviour = DirectionBehaviourPair.Behaviour
				state.Direction = Direction(DirectionBehaviourPair.Direction)
				newLocalStateChannel <- state
				switch state.Behaviour {
				case DoorOpen:
					//start timer på nytt og rydd forespørsler i nåværende etasje
					resetTimerChannel <- true
					OrderCompletedatCurrentFloor(state.Floor, Direction(state.Direction.convertMD()), completedOrderChannel) //requests cleared
					newLocalStateChannel <- state
				case Moving, Idle:
					elevio.SetDoorOpenLamp(false)
					elevio.SetMotorDirection(DirectionBehaviourPair.Direction)
				}
			}
		}
		elevio.SetDoorOpenLamp(state.Behaviour == DoorOpen)
	}
}

/*
default/panic bør det implementeres over alt?
obstruction - ??
doesnt know its in between two floors when stopping in between two floors
printer new orders selv om vi ikke har noen orders?
*/
