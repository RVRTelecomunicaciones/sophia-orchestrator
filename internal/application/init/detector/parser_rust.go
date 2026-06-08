package detector

import (
	"bufio"
	"bytes"
	"strings"
)

// parseCargoToml extracts Rust package information from Cargo.toml content.
func parseCargoToml(content []byte) *LanguageInfo {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	var edition string
	inPackage := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "[package]" {
			inPackage = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inPackage = false
		}
		if inPackage && strings.HasPrefix(line, "edition") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				edition = strings.Trim(strings.TrimSpace(parts[1]), `"`)
			}
		}
	}
	evidence := "Cargo.toml"
	if edition != "" {
		evidence = "Cargo.toml (edition " + edition + ")"
	}
	return &LanguageInfo{
		Name:            "Rust",
		VersionEvidence: evidence,
	}
}
