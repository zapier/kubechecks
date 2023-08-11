package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

type DocOpt[D any] struct {
	choices      []string
	defaultValue *D
	shorthand    *string
}

func combine[D any](dst *DocOpt[D], src DocOpt[D]) {
	if src.choices != nil {
		dst.choices = src.choices
	}
	if src.defaultValue != nil {
		dst.defaultValue = src.defaultValue
	}
	if src.shorthand != nil {
		dst.shorthand = src.shorthand
	}
}

func ViperNameToEnv(s string) string {
	s = envKeyReplacer.Replace(s)
	s = fmt.Sprintf("%s_%s", envPrefix, s)
	s = strings.ToUpper(s)
	return s
}

func boolFlag(flags *pflag.FlagSet, name, usage string, opts ...DocOpt[bool]) {
	addFlag(name, usage, opts, flags.Bool, flags.BoolP)
}

func newStringOpts() DocOpt[string] {
	return DocOpt[string]{}
}

func stringFlag(flags *pflag.FlagSet, name, usage string, opts ...DocOpt[string]) {
	addFlag(name, usage, opts, flags.String, flags.StringP)
}

func addFlag[D any](
		name, usage string,
		opts []DocOpt[D],
		onlyLong func(string, D, string) *D,
		longAndShort func(string, string, D, string) *D,
) {
	var opt DocOpt[D]
	for _, o := range opts {
		combine(&opt, o)
	}

	usage = generateUsage(opt, usage, name)
	var defaultValue D
	if opt.defaultValue != nil {
		defaultValue = *opt.defaultValue
	}

	if opt.shorthand != nil {
		longAndShort(name, *opt.shorthand, defaultValue, usage)
	} else {
		onlyLong(name, defaultValue, usage)
	}
}

func generateUsage[D any](opt DocOpt[D], usage string, name string) string {
	if !strings.HasSuffix(usage, ".") {
		panic(fmt.Sprintf("usage for %q must end with a period.", name))
	}

	if opt.choices != nil {
		choices := opt.choices
		sort.Strings(choices)
		usage = fmt.Sprintf("%s One of %s.", usage, strings.Join(choices, ", "))
	}

	envVar := ViperNameToEnv(name)
	usage = fmt.Sprintf("%s (%s)", usage, envVar)
	return usage
}

func (d DocOpt[D]) withDefault(def D) DocOpt[D] {
	d.defaultValue = &def
	return d
}

func (d DocOpt[D]) withShortHand(short string) DocOpt[D] {
	d.shorthand = &short
	return d
}

func (d DocOpt[D]) withChoices(choices ...string) DocOpt[D] {
	d.choices = choices
	return d
}
