package pptpolish

func BuildFallbackMarkdown(brief string) (string, error) {
	return buildPPTPolishFallbackMarkdown(brief)
}

func AudiencePageCount(brief string) int {
	return pptPolishAudiencePageCount(brief)
}

func SelectFallbackTheme(userInput, brief string) string {
	return selectPPTPolishFallbackTheme(userInput, brief)
}

func ApplyFallbackTheme(markdown, theme string) string {
	return applyPPTPolishFallbackTheme(markdown, theme)
}

func FallbackThemeLabel(theme string) string {
	return pptPolishFallbackThemeLabel(theme)
}

func AudienceContentViolations(text string) []string {
	return pptPolishAudienceContentViolations(text)
}

func UserVisiblePath(target, workspace string) string {
	return pptPolishUserVisiblePath(target, workspace)
}
