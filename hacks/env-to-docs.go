package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/zapier/kubechecks/cmd"
)

type option struct {
	Option  string
	Env     string
	Usage   string
	Default string
}

var UsageEnvVar = regexp.MustCompile(` \(KUBECHECKS_[_A-Z0-9]+\)`)
var UsageDefaultValue = regexp.MustCompile(`Defaults to \.?(.*)+\.`)

func main() {
	outputFilename := filepath.Join("docs", "usage.md")
	templateFilename := outputFilename + ".tpl"

	data, err := os.ReadFile(templateFilename)
	if err != nil {
		panic(err)
	}
	t, err := template.New("usage").Parse(string(data))
	if err != nil {
		panic(err)
	}

	_, err = os.Stat(outputFilename)
	if err != nil && !os.IsNotExist(err) {
		panic(err)
	} else if err == nil {
		if err = os.Remove(outputFilename); err != nil {
			panic(err)
		}
	}

	flagUsage := make(map[string]option)

	cleanUpUsage := func(s string) string {
		s = UsageEnvVar.ReplaceAllString(s, "")
		s = UsageDefaultValue.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		return s
	}

	visitFlag := func(flag *pflag.Flag) {
		flagUsage[flag.Name] = option{
			Default: flag.DefValue,
			Env:     cmd.ViperNameToEnv(flag.Name),
			Option:  flag.Name,
			Usage:   cleanUpUsage(flag.Usage),
		}
	}

	addFlags(cmd.RootCmd, visitFlag)
	addFlags(cmd.ControllerCmd, visitFlag)

	vars := getSortedFlags(flagUsage)

	f, err := os.OpenFile(outputFilename, os.O_WRONLY|os.O_CREATE, 0o666)
	if err != nil {
		panic(err)
	}

	type templateVars struct {
		Options []option
	}
	if err = t.Execute(f, templateVars{vars}); err != nil {
		panic(err)
	}
}

func getSortedFlags(flagUsage map[string]option) []option {
	var keys []string
	for key := range flagUsage {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var vars []option
	for _, key := range keys {
		vars = append(vars, flagUsage[key])
	}
	return vars
}

func addFlags(cmd *cobra.Command, visitFlag func(flag *pflag.Flag)) {
	cmd.Flags().VisitAll(visitFlag)
	cmd.PersistentFlags().VisitAll(visitFlag)
}
