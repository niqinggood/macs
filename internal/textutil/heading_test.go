package textutil

import "testing"

func TestHeadingPrefixRecognition(t *testing.T) {
	for _, value := range []string{"1. Title", "1.2 Scope", "2 Results"} {
		if !IsNumberedHeading(value) {
			t.Errorf("IsNumberedHeading(%q) = false", value)
		}
	}
	for _, value := range []string{"一、背景", "第三章 方法"} {
		if !IsChineseNumberedHeading(value) {
			t.Errorf("IsChineseNumberedHeading(%q) = false", value)
		}
	}
}
