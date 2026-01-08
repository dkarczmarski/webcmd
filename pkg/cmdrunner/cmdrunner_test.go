package cmdrunner_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner/internal/mocks"
	"go.uber.org/mock/gomock"
)

func TestRunCommandWithRunner(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		ctx := t.Context()
		cmdName := "echo"
		args := []string{"hello"}

		mockRunner.EXPECT().
			Command(gomock.Any(), cmdName, "hello").
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
			_, _ = w.Write([]byte("hello output"))
		})
		mockCommand.EXPECT().SetStderr(gomock.Any())
		mockCommand.EXPECT().Run().Return(nil)
		mockCommand.EXPECT().ProcessState().Return(nil) // nil means exit code 0 in our logic if err is nil

		var output bytes.Buffer
		exitCode, err := cmdrunner.RunCommandWithRunner(ctx, mockRunner, cmdName, args, &output)
		//
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if exitCode != 0 {
			t.Errorf("expected exit code 0, got %d", exitCode)
		}

		if output.String() != "hello output" {
			t.Errorf("expected output 'hello output', got %q", output.String())
		}
	})

	t.Run("command failure", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		ctx := t.Context()
		cmdName := "false"
		args := []string{}

		mockRunner.EXPECT().
			Command(gomock.Any(), cmdName).
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any())
		mockCommand.EXPECT().SetStderr(gomock.Any())

		// We can't easily create a real exec.ExitError with a specific exit code for mocking
		// without actually running a command, but we can return a generic error and expect -1.
		// Or if we want to test ExitCode() extraction, we'd need a real ExitError.
		mockCommand.EXPECT().Run().Return(errors.New("some error"))

		var output bytes.Buffer
		exitCode, err := cmdrunner.RunCommandWithRunner(ctx, mockRunner, cmdName, args, &output)

		if err == nil {
			t.Error("expected error, got nil")
		}

		if exitCode != -1 {
			t.Errorf("expected exit code -1 for generic error, got %d", exitCode)
		}
	})
}
