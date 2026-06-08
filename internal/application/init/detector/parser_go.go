package detector

import (
	"bufio"
	"bytes"
	"strings"
)

// parseGoMod extracts language and module information from go.mod content.
// Returns a LanguageInfo if the file looks like a valid go.mod.
func parseGoMod(content []byte) *LanguageInfo {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	var goVersion string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "go ") {
			goVersion = strings.TrimPrefix(line, "go ")
			goVersion = strings.TrimSpace(goVersion)
			break
		}
	}
	version := goVersion
	if version == "" {
		version = "unknown"
	}
	return &LanguageInfo{
		Name:            "Go",
		VersionEvidence: "go " + version,
	}
}
