package detector

import (
	"bufio"
	"bytes"
	"strings"
)

// parsePyprojectToml detects Python frameworks from pyproject.toml content.
// FastAPI, Django, Flask fingerprints are checked in the dependencies list.
func parsePyprojectToml(content []byte) []FrameworkInfo {
	var frameworks []FrameworkInfo

	scanner := bufio.NewScanner(bytes.NewReader(content))
	inDeps := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Detect entry into [project.dependencies] or "dependencies = [" section.
		if line == "[project]" || strings.HasPrefix(line, "dependencies") {
			inDeps = true
		}
		if strings.HasPrefix(line, "[") && line != "[project]" && !strings.HasPrefix(line, "[project.") {
			inDeps = false
		}

		lower := strings.ToLower(line)
		if strings.Contains(lower, "fastapi") {
			frameworks = appendIfMissing(frameworks, FrameworkInfo{
				Name:         "FastAPI",
				EvidencePath: "pyproject.toml",
			})
		}
		if strings.Contains(lower, "django") {
			frameworks = appendIfMissing(frameworks, FrameworkInfo{
				Name:         "Django",
				EvidencePath: "pyproject.toml",
			})
		}
		if strings.Contains(lower, "flask") {
			frameworks = appendIfMissing(frameworks, FrameworkInfo{
				Name:         "Flask",
				EvidencePath: "pyproject.toml",
			})
		}
		_ = inDeps
	}
	return frameworks
}

// parseRequirementsTxt detects Python frameworks from requirements.txt content.
func parseRequirementsTxt(content []byte) []FrameworkInfo {
	var frameworks []FrameworkInfo
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "fastapi") {
			frameworks = appendIfMissing(frameworks, FrameworkInfo{Name: "FastAPI", EvidencePath: "requirements.txt"})
		}
		if strings.HasPrefix(line, "django") {
			frameworks = appendIfMissing(frameworks, FrameworkInfo{Name: "Django", EvidencePath: "requirements.txt"})
		}
		if strings.HasPrefix(line, "flask") {
			frameworks = appendIfMissing(frameworks, FrameworkInfo{Name: "Flask", EvidencePath: "requirements.txt"})
		}
	}
	return frameworks
}

// appendIfMissing adds fw to slice only if no entry with the same Name exists.
func appendIfMissing(slice []FrameworkInfo, fw FrameworkInfo) []FrameworkInfo {
	for _, f := range slice {
		if f.Name == fw.Name {
			return slice
		}
	}
	return append(slice, fw)
}
