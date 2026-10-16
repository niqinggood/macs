package pptpolish

import (
	"fmt"

	contenttools "mac/internal/tools/content"
)

const GoogleCleanPrimaryHex = "4285F4"

func RequiresGoogleStyle(userInput string) bool {
	return selectPPTPolishFallbackTheme(userInput, "") == "google_style"
}

func PPTXContainsSRGBColor(path, colorHex string) (bool, error) {
	return contenttools.PPTXContainsSRGBColor(path, colorHex)
}

func ValidateRequestedTheme(userInput, pptxPath string) error {
	if !RequiresGoogleStyle(userInput) {
		return nil
	}
	found, err := PPTXContainsSRGBColor(pptxPath, GoogleCleanPrimaryHex)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("PPTX visual-theme validation failed: explicit Google-clean style requires primary blue %s", GoogleCleanPrimaryHex)
	}
	return nil
}
