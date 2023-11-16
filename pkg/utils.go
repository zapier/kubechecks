package pkg

var (
	GitTag    = ""
	GitCommit = ""
)

func Pointer[T interface{}](item T) *T {
	return &item
}
