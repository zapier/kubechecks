package cmd

import (
	"fmt"
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

func newBoolOpts() DocOpt[bool] {
	return DocOpt[bool]{}
}

func newInt64Opts() DocOpt[int64] { return DocOpt[int64]{} }

func newStringOpts() DocOpt[string] {
	return DocOpt[string]{}
}

func newStringSliceOpts() DocOpt[[]string] {
	return DocOpt[[]string]{}
}

func int64Flag(flags *pflag.FlagSet, name, usage string, opts ...DocOpt[int64]) {
	addFlag(name, usage, opts, flags.Int64, flags.Int64P)
}

func stringFlag(flags *pflag.FlagSet, name, usage string, opts ...DocOpt[string]) {
	addFlag(name, usage, opts, flags.String, flags.StringP)
}

func stringSliceFlag(flags *pflag.FlagSet, name, usage string, opts ...DocOpt[[]string]) {
	addFlag(name, usage, opts, flags.StringArray, flags.StringArrayP)
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
		usage = fmt.Sprintf("%s One of %s.", usage, strings.Join(opt.choices, ", "))
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
