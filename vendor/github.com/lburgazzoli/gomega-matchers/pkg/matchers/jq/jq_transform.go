package jq

import (
	"fmt"

	"github.com/itchyny/gojq"
)

// Extract returns a transform function that extracts a value from input using a JQ expression.
// The returned function can be used with Gomega's WithTransform matcher combinator.
//
// Example:
//
//	WithTransform(jq.Extract(`.status`), Equal("ready"))
func Extract(expression string) func(in any) (any, error) {
	return func(in any) (any, error) {
		query, err := gojq.Parse(expression)
		if err != nil {
			return nil, fmt.Errorf("unable to parse expression %s, %w", expression, err)
		}

		data, err := Convert(in)
		if err != nil {
			return false, err
		}

		return Run(query, data)
	}
}
