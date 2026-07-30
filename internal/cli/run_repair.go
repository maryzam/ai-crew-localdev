package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/maryzam/ai-crew-localdev/internal/control"
)

func offerHTTPSRepair(out io.Writer, in io.Reader, sshErr *control.SSHRemoteError) (bool, error) {
	httpsURL := sshErr.HTTPSURL()
	question := fmt.Sprintf("Repository origin uses an SSH remote; managed runs require HTTPS. Switch origin (fetch and push) to %s now?", httpsURL)
	if !promptYesNoLine(out, in, question) {
		return false, nil
	}
	if err := setHTTPSRemote(sshErr.RootPath, httpsURL); err != nil {
		return false, err
	}
	_, _ = fmt.Fprintf(out, "switched origin to %s\n", httpsURL)
	return true, nil
}

func setHTTPSRemote(rootPath, httpsURL string) error {
	for _, args := range [][]string{
		{"-C", rootPath, "remote", "set-url", "origin", httpsURL},
		{"-C", rootPath, "remote", "set-url", "--push", "origin", httpsURL},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("switch origin to HTTPS: %w (%s)", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func promptYesNoLine(out io.Writer, in io.Reader, question string) bool {
	_, _ = fmt.Fprintf(out, "%s [y/N] ", question)
	answer, err := readOneLine(in)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(answer), "y")
}

func readOneLine(in io.Reader) (string, error) {
	var builder strings.Builder
	buffer := make([]byte, 1)
	for {
		count, err := in.Read(buffer)
		if count > 0 {
			if buffer[0] == '\n' {
				return builder.String(), nil
			}
			builder.WriteByte(buffer[0])
		}
		if err != nil {
			if builder.Len() > 0 {
				return builder.String(), nil
			}
			return "", err
		}
	}
}

func isTerminalReader(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}
