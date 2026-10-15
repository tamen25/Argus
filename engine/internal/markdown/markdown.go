// Package markdown holds what the engine's Markdown renderers share.
package markdown

import "strings"

// Cell makes s safe inside a Markdown table cell. A value that comes from
// telemetry, such as a service name or a team label, can hold any text: a "|"
// would end the cell early and a line break would end the row, so every later
// column would shift. "|" and "\" are escaped, and line breaks become spaces.
func Cell(s string) string {
	return cellEscaper.Replace(s)
}

// strings.Replacer makes one pass and never rescans its own output, so the
// backslash added before a "|" is not escaped again.
var cellEscaper = strings.NewReplacer(
	`\`, `\\`,
	"|", `\|`,
	"\r\n", " ",
	"\n", " ",
	"\r", " ",
)
