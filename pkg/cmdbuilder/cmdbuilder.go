// Package cmdbuilder provide logic for building commands.
package cmdbuilder

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// Result represents the result of building a command.
type Result struct {
	Command   string
	Arguments []string
}

// BuildCommand builds a command from a pattern and parameters.
func BuildCommand(commandTemplate string, params map[string]interface{}) (Result, error) {
	tmpl, err := template.New("command").Parse(commandTemplate)
	if err != nil {
		return Result{}, fmt.Errorf("error parsing template: %w", err)
	}

	var buf bytes.Buffer

	err = tmpl.Execute(&buf, params)
	if err != nil {
		return Result{}, fmt.Errorf("error executing template: %w", err)
	}

	rawLines := strings.Split(buf.String(), "\n")

	var lines []string

	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}

	var (
		command   string
		arguments []string
	)

	if len(lines) > 0 {
		command = lines[0]

		if len(lines) > 1 {
			arguments = lines[1:]
		}
	}

	return Result{
		Command:   command,
		Arguments: arguments,
	}, nil
}
