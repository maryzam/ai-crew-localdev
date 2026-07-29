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

func ensureHTTPSRemote(out io.Writer, in io.Reader, interactive bool, repoPath string) error {
	repo, err := control.ResolveRepository(repoPath)
	if err != nil || !repo.SSH {
		return nil
	}
	if !interactive {
		return nil
	}
	httpsURL := "https://github.com/" + repo.Slug + ".git"
	question := fmt.Sprintf("Repository origin is an SSH remote (%s); managed runs require HTTPS. Switch origin to %s now?", repo.Remote, httpsURL)
	if !promptYesNoLine(out, in, question) {
		return nil
	}
	if output, err := exec.Command("git", "-C", repo.RootPath, "remote", "set-url", "origin", httpsURL).CombinedOutput(); err != nil {
		return fmt.Errorf("switch origin to HTTPS: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	_, _ = fmt.Fprintf(out, "switched origin to %s\n", httpsURL)
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
