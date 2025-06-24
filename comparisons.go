package shadow

import (
	"fmt"
	"github.com/itchyny/gojq"
	"slices"
)

type LogLevel string

const levelDebug LogLevel = "debug"
const levelInfo LogLevel = "info"
const levelError LogLevel = "error"

func (ll *LogLevel) UnmarshalJSON(b []byte) error {
	if !slices.Contains([]LogLevel{levelDebug, levelInfo, levelError}, LogLevel(b)) {
		return fmt.Errorf("unknown log level: %s", string(b))
	}

	*ll = LogLevel(b)
	return nil
}

type JQQuery string

func (q *JQQuery) UnmarshalJSON(b []byte) error {
	_, err := gojq.Parse(string(b))
	return err
}

type ComparisonConfig struct {
	Status     bool
	Body       bool
	Headers    []string
	JSON       []JQQuery
	IgnoreJSON []JQQuery

	json       []*gojq.Query
	ignoreJSON []*gojq.Query
}

type ReportingConfig struct {
	NoLog      bool
	LogLevel   *LogLevel
	RedactJSON []JQQuery

	redactJSON []*gojq.Query
}
