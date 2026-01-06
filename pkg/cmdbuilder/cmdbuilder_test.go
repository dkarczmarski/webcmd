package cmdbuilder_test

import (
	"reflect"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/cmdbuilder"
)

func TestBuildCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		commandTemplate string
		params          map[string]interface{}
		want            cmdbuilder.Result
		wantErr         bool
	}{
		{
			name:            "simple command",
			commandTemplate: "echo\nhello\nworld",
			params:          nil,
			want: cmdbuilder.Result{
				Command:   "echo",
				Arguments: []string{"hello", "world"},
			},
			wantErr: false,
		},
		{
			name:            "command with empty lines",
			commandTemplate: "\necho\n\nhello\n\nworld\n",
			params:          nil,
			want: cmdbuilder.Result{
				Command:   "echo",
				Arguments: []string{"hello", "world"},
			},
			wantErr: false,
		},
		{
			name:            "template with params and empty lines",
			commandTemplate: "{{.Cmd}}\n\n{{.Arg1}}\n\n{{.Arg2}}",
			params: map[string]interface{}{
				"Cmd":  "ls",
				"Arg1": "-l",
				"Arg2": "-a",
			},
			want: cmdbuilder.Result{
				Command:   "ls",
				Arguments: []string{"-l", "-a"},
			},
			wantErr: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := cmdbuilder.BuildCommand(testCase.commandTemplate, testCase.params)
			if (err != nil) != testCase.wantErr {
				t.Errorf("BuildCommand() error = %v, wantErr %v", err, testCase.wantErr)

				return
			}

			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("BuildCommand() got = %v, want %v", got, testCase.want)
			}
		})
	}
}
