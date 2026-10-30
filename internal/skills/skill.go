// Package skills gives agents the skills a project keeps: directories of
// instructions in the Agent Skills format (https://agentskills.io), each
// with a SKILL.md, under .devpad/skills/ or .claude/skills/ at the root of
// the repo.
//
// A run sees each skill's name and description in its system prompt and
// loads the skills a task calls for with the load_skill tool. The tool runs
// on the host and reads from where the catalog came from: the run's
// workspace, except in a review sandbox, whose skills come from the merge
// request's target branch in the canonical repo, never from the change under
// review. The same goes for a review sandbox's AGENTS.md. A skill only adds
// instructions: allowed-tools, and every other field that would change what
// an agent may do, is ignored.
package skills

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Roots are the directories skills are found in, relative to the repo root.
// A skill shadows one of the same name in a later root.
var Roots = []string{".devpad/skills", ".claude/skills"}

// skillFile is the file that makes a directory a skill.
const skillFile = "SKILL.md"

// maxDescriptionBytes is the Agent Skills format's limit on a description,
// which longer ones are cut to.
const maxDescriptionBytes = 1024

// namePattern is what a skill's directory may be called. The format asks
// for lowercase letters, digits and hyphens; capitals and underscores are
// let through for skills written for other agents, while dots, slashes and
// spaces never are, so a name is always one safe path segment and one word
// to type after a slash.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Skill is one skill a workspace or branch offers.
type Skill struct {
	// Name is the skill's directory name, which is what the format requires
	// its name field to be and what the user types after a slash.
	Name        string `json:"name"`
	Description string `json:"description"`
	// ArgumentHint describes what the user may type after the name.
	ArgumentHint string `json:"argumentHint,omitempty"`
	// ModelInvocable is false for a skill the model is not told about, which
	// only the user can start.
	ModelInvocable bool `json:"modelInvocable"`
	// UserInvocable is false for a skill the IDE leaves out of its slash
	// commands, which only the model loads.
	UserInvocable bool `json:"userInvocable"`
	// Dir is the skill's directory, relative to the repo root.
	Dir string `json:"-"`
}

// parseSkill reads a skill's SKILL.md. Like other agents that read the
// format, it is lenient: frontmatter that is not valid YAML is read line by
// line, and a skill without a description takes the first line of its
// instructions. Only a skill with neither is refused.
func parseSkill(name, dir string, data []byte) (Skill, bool) {
	front, body := splitFrontmatter(string(data))
	fields := frontmatterFields(front)
	description := stringField(fields["description"])
	if when := stringField(fields["when_to_use"]); when != "" {
		description = strings.TrimSpace(description + " " + when)
	}
	if description == "" {
		description = firstLine(body)
	}
	description = clip(strings.Join(strings.Fields(description), " "), maxDescriptionBytes)
	if description == "" {
		return Skill{}, false
	}
	return Skill{
		Name:           name,
		Description:    description,
		ArgumentHint:   clip(stringField(fields["argument-hint"]), 200),
		ModelInvocable: !boolField(fields["disable-model-invocation"], false),
		UserInvocable:  boolField(fields["user-invocable"], true),
		Dir:            dir,
	}, true
}

// skillBody returns a SKILL.md's instructions, without its frontmatter.
func skillBody(data []byte) string {
	_, body := splitFrontmatter(string(data))
	return strings.TrimSpace(body)
}

// splitFrontmatter splits the YAML frontmatter between the leading "---"
// lines from the Markdown after it. Without both lines it is all body.
func splitFrontmatter(text string) (front, body string) {
	text = strings.TrimPrefix(text, "\uFEFF")
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || strings.TrimRight(first, " \t\r") != "---" {
		return "", text
	}
	var b strings.Builder
	for rest != "" {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		if strings.TrimRight(line, " \t\r") == "---" {
			return b.String(), rest
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return "", text
}

// frontmatterFields decodes the frontmatter. Skills written for other
// agents often hold YAML those agents accept but a parser does not, such as
// "description: Use when: ..." unquoted; their top-level fields are read one
// line at a time instead.
func frontmatterFields(front string) map[string]any {
	if strings.TrimSpace(front) == "" {
		return nil
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(front), &fields); err == nil {
		return fields
	}
	fields = map[string]any{}
	for _, line := range strings.Split(front, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		fields[strings.TrimSpace(key)] = value
	}
	return fields
}

// stringField reads a scalar field as text. A list, as in the common
// `argument-hint: [issue]`, is shown as written.
func stringField(v any) string {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s := stringField(item); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return ""
}

// boolField reads true/false, yes/no, on/off or 1/0, in any case, as other
// agents do; anything else is the default.
func boolField(v any, def bool) bool {
	switch v := v.(type) {
	case bool:
		return v
	case int:
		if v == 0 || v == 1 {
			return v == 1
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on", "1":
			return true
		case "false", "no", "off", "0":
			return false
		}
	}
	return def
}

// firstLine returns the first line of text in Markdown, without heading
// marks.
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); line != "" {
			return line
		}
	}
	return ""
}

// clip cuts s to at most n bytes, on a character boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n-len("…")]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}
