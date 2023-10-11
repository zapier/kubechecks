package pkg

var (
	GitTag    = ""
	GitCommit = ""
)

func PassEmoji() string {
	return " :white_check_mark: "
}
func PassString() string {
	return " Passed" + PassEmoji()
}

func WarningEmoji() string {
	return " :warning: "
}

func WarningString() string {
	return " Warning" + WarningEmoji()
}

func FailedEmoji() string {
	return " :red_circle: "
}

func FailedString() string {
	return " Failed" + FailedEmoji()
}

func Pointer[T interface{}](item T) *T {
	return &item
}
