package harness

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type compiledCheck struct {
	field     string
	regex     *regexp.Regexp
	min       *float64
	max       *float64
	equals    any
	hasEquals bool
}

func compileChecks(schema *Schema, table string, specs []CheckSpec) (PeerCheck, error) {
	if table != "" && schema.Object(table) == nil {
		return nil, fmt.Errorf("harness: unknown check table %q", table)
	}
	if table == "" && len(specs) == 0 {
		return nil, nil
	}

	compiled := make([]compiledCheck, 0, len(specs))
	for _, spec := range specs {
		if spec.Field == "" {
			return nil, fmt.Errorf("harness: check without a field")
		}
		check := compiledCheck{
			field: spec.Field,
			min:   spec.Min,
			max:   spec.Max,
		}
		if spec.Regex != "" {
			re, err := regexp.Compile(spec.Regex)
			if err != nil {
				return nil, fmt.Errorf("harness: check %s regex: %w", spec.Field, err)
			}
			check.regex = re
		}
		if len(spec.Equals) > 0 {
			if err := json.Unmarshal(spec.Equals, &check.equals); err != nil {
				return nil, fmt.Errorf("harness: check %s equals: %w", spec.Field, err)
			}
			check.hasEquals = true
		}
		compiled = append(compiled, check)
	}

	return func(frame []byte) error {
		if table != "" {
			if err := schema.ValidateFor(frame, table); err != nil {
				return err
			}
		}
		for _, check := range compiled {
			var (
				value any
				err   error
			)
			if strings.Contains(check.field, ".") {
				value, err = schema.ReadFieldPath(frame, check.field)
			} else {
				value, err = schema.ReadFieldFor(frame, table, check.field)
			}
			if err != nil {
				return err
			}
			if err := check.validate(value); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func (c compiledCheck) validate(value any) error {
	if c.regex != nil {
		str, ok := value.(string)
		if !ok {
			return fmt.Errorf("field %s is not a string", c.field)
		}
		if !c.regex.MatchString(str) {
			return fmt.Errorf("field %s does not match %s", c.field, c.regex)
		}
	}
	if c.min != nil || c.max != nil {
		number, err := numericValue(value)
		if err != nil {
			return fmt.Errorf("field %s: %w", c.field, err)
		}
		if c.min != nil && number < *c.min {
			return fmt.Errorf("field %s = %v is below %v", c.field, number, *c.min)
		}
		if c.max != nil && number > *c.max {
			return fmt.Errorf("field %s = %v is above %v", c.field, number, *c.max)
		}
	}
	if c.hasEquals && !valuesEqual(value, c.equals) {
		return fmt.Errorf("field %s = %v, want %v", c.field, value, c.equals)
	}
	return nil
}

func numericValue(value any) (float64, error) {
	switch v := value.(type) {
	case int8:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	default:
		return 0, fmt.Errorf("is not numeric")
	}
}

func valuesEqual(got, want any) bool {
	switch w := want.(type) {
	case string:
		g, ok := got.(string)
		return ok && g == w
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case nil:
		return got == nil
	default:
		g, err := numericValue(got)
		if err != nil {
			return false
		}
		wv, err := numericValue(want)
		return err == nil && g == wv
	}
}
