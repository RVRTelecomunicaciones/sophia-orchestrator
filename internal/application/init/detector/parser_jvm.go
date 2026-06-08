package detector

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// parseBuildGradle detects JVM languages and frameworks from build.gradle content.
// Spring Boot fingerprint: "spring-boot-starter" in dependencies.
func parseBuildGradle(content []byte) ([]LanguageInfo, []FrameworkInfo) {
	var languages []LanguageInfo
	var frameworks []FrameworkInfo

	text := string(content)

	// Kotlin detection.
	if strings.Contains(text, "kotlin") {
		languages = append(languages, LanguageInfo{
			Name:            "Kotlin",
			VersionEvidence: "build.gradle",
		})
	} else {
		// Default: Java.
		languages = append(languages, LanguageInfo{
			Name:            "Java",
			VersionEvidence: "build.gradle",
		})
	}

	// Spring Boot fingerprint.
	if strings.Contains(text, "spring-boot-starter") {
		frameworks = append(frameworks, FrameworkInfo{
			Name:         "Spring Boot",
			EvidencePath: "build.gradle",
		})
	}

	// Micronaut fingerprint.
	if strings.Contains(text, "micronaut") {
		frameworks = append(frameworks, FrameworkInfo{
			Name:         "Micronaut",
			EvidencePath: "build.gradle",
		})
	}

	return languages, frameworks
}

// pomXML is a minimal shape for parsing Maven pom.xml.
type pomXML struct {
	XMLName    xml.Name `xml:"project"`
	Parent     pomParent `xml:"parent"`
	Properties pomProps  `xml:"properties"`
}

type pomParent struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

type pomProps struct {
	JavaVersion string `xml:"java.version"`
}

// parsePomXML detects JVM languages and frameworks from Maven pom.xml.
// Spring Boot fingerprint: parent groupId "org.springframework.boot".
func parsePomXML(content []byte) ([]LanguageInfo, []FrameworkInfo) {
	var languages []LanguageInfo
	var frameworks []FrameworkInfo

	var pom pomXML
	if err := xml.Unmarshal(content, &pom); err != nil {
		// Fallback: text scan.
		text := string(content)
		if strings.Contains(text, "spring-boot") {
			frameworks = append(frameworks, FrameworkInfo{Name: "Spring Boot", EvidencePath: "pom.xml"})
		}
		languages = append(languages, LanguageInfo{Name: "Java", VersionEvidence: "pom.xml"})
		return languages, frameworks
	}

	// Java is the default JVM language for Maven projects.
	javaVersion := pom.Properties.JavaVersion
	languages = append(languages, LanguageInfo{
		Name:            "Java",
		VersionEvidence: javaVersion,
	})

	if pom.Parent.GroupID == "org.springframework.boot" {
		frameworks = append(frameworks, FrameworkInfo{
			Name:         "Spring Boot",
			Version:      pom.Parent.Version,
			EvidencePath: "pom.xml",
		})
	}

	// Text-scan fallback for Spring Boot in dependencies section.
	if len(frameworks) == 0 && bytes.Contains(content, []byte("spring-boot")) {
		frameworks = append(frameworks, FrameworkInfo{Name: "Spring Boot", EvidencePath: "pom.xml"})
	}

	return languages, frameworks
}
