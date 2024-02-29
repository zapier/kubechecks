package events

var inFlight int32

func GetInFlight() int {
	return int(inFlight)
}
