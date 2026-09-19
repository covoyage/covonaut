//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func trashPathPlatform(abs string, info os.FileInfo) error {
	method := "DeleteFile"
	if info.IsDir() {
		method = "DeleteDirectory"
	}
	script := fmt.Sprintf(
		`[Microsoft.VisualBasic.FileIO.FileSystem]::%s(%s, 'OnlyErrorDialogs', 'SendToRecycleBin')`,
		method, powershellString(abs),
	)
	ps := resolvePowerShell()
	if ps == "" {
		return fmt.Errorf("powershell is required to send files to the Recycle Bin")
	}
	cmd := exec.Command(ps, "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("recycle bin: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func powershellString(s string) string {
	s = strings.ReplaceAll(s, `'`, `''`)
	return `'` + s + `'`
}
