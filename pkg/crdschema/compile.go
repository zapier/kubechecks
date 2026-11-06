package crdschema

import (
	"bytes"

	"github.com/pkg/errors"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// compiles reports whether kubeconform will be able to use this schema, by compiling it
// the same way kubeconform does.
//
// It exists because of how kubeconform reacts to a schema it cannot compile: it moves on
// to the next registry without surfacing the error, and if no other registry has the kind
// the resource is reported as "could not find schema for <kind>". That reaches the pull
// request as a check failure naming the wrong problem. Rejecting the schema here instead
// leaves the kind to resolve through the remaining schema locations exactly as it would
// have if the repository had never defined it, and puts the real reason in the log.
func compiles(name string, encoded []byte) error {
	// A document that decodes to nil is accepted by AddResource but makes Compile
	// dereference a nil root and panic, which kubeconform only started guarding against
	// in v0.8.0.
	if len(bytes.TrimSpace(encoded)) == 0 {
		return errors.New("schema is empty")
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return errors.Wrap(err, "schema is not valid JSON")
	}
	if doc == nil {
		return errors.New("schema is null")
	}

	compiler := jsonschema.NewCompiler()
	// kubeconform pins draft-04, which is also the dialect OpenAPI 3.0 schema objects are
	// written in. Compiling under any other draft here would accept schemas kubeconform
	// then rejects, which is the failure this check exists to prevent.
	compiler.DefaultDraft(jsonschema.Draft4)

	if err := compiler.AddResource(name, doc); err != nil {
		return errors.Wrap(err, "schema could not be loaded")
	}
	if _, err := compiler.Compile(name); err != nil {
		return errors.Wrap(err, "schema could not be compiled")
	}

	return nil
}
