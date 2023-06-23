package affected_apps

import (
	"reflect"
	"sort"
	"testing"
)

func Test_modifiedDirs(t *testing.T) {
	type args struct {
		changeList []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			"basic",
			args{
				changeList: []string{
					"foo/bar/file.txt",
					"foo/bar/file2.yaml",
					"foo/baz/thing",
				},
			},
			[]string{
				"foo/bar",
				"foo/baz",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modifiedDirs(tt.args.changeList)
			sort.Strings(got)
			sort.Strings(tt.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("modifiedDirs() = %v, want %v", got, tt.want)
			}
		})
	}
}
