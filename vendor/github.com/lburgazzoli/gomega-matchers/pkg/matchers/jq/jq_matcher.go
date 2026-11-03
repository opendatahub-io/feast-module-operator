package jq

import (
	"fmt"

	"github.com/itchyny/gojq"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
)

// Match creates a Gomega matcher that evaluates a JQ expression against the actual value.
// The expression should return a boolean result. Supports format string with args for dynamic expressions.
//
// Example:
//
//	Expect(`{"a":1}`).Should(jq.Match(`.a == 1`))
//	Expect(data).Should(jq.Match(`.status.phase == "%s"`, "Running"))
func Match(f string, args ...any) types.GomegaMatcher {
	return &jqMatcher{
		Expression: fmt.Sprintf(f, args...),
	}
}

var _ types.GomegaMatcher = &jqMatcher{}

type jqMatcher struct {
	Expression       string
	firstFailurePath []any
}

func (matcher *jqMatcher) Match(actual any) (bool, error) {
	query, err := gojq.Parse(matcher.Expression)
	if err != nil {
		return false, fmt.Errorf("unable to parse expression %s, %w", matcher.Expression, err)
	}

	data, err := Convert(actual)
	if err != nil {
		return false, err
	}

	it := query.Run(data)

	v, ok := it.Next()
	if !ok {
		return false, nil
	}

	if err, ok := v.(error); ok {
		return false, gomega.StopTrying(err.Error())
	}

	if match, ok := v.(bool); ok {
		return match, nil
	}

	return false, nil
}

func (matcher *jqMatcher) FailureMessage(actual any) string {
	a := fmt.Sprintf("%v", actual)
	m := format.Message(a, "to match expression", matcher.Expression)

	return formattedMessage(m, matcher.firstFailurePath)
}

func (matcher *jqMatcher) NegatedFailureMessage(actual any) string {
	a := fmt.Sprintf("%v", actual)
	m := format.Message(a, "not to match expression", matcher.Expression)

	return formattedMessage(m, matcher.firstFailurePath)
}
