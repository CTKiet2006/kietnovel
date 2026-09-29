package domain

import (
	"fmt"
	"unicode/utf8"
)

// ReviewInterval is the global review interval (triggered once every N chapters).
const ReviewInterval = 5

// ShouldReview decides whether a global review is needed based on the number of completed chapters (short/medium mode).
func ShouldReview(completedCount int) (bool, string) {
	if completedCount > 0 && completedCount%ReviewInterval == 0 {
		return true, fmt.Sprintf("已完成 %d 章，触发全局审阅", completedCount)
	}
	return false, ""
}

// ShouldArcReview decides in long-form mode whether an arc-level/volume-level review is needed.
func ShouldArcReview(isArcEnd, isVolumeEnd bool, volume, arc int) (bool, string) {
	if isVolumeEnd {
		return true, fmt.Sprintf("第 %d 卷第 %d 弧结束（卷结束），触发弧级+卷级评审", volume, arc)
	}
	if isArcEnd {
		return true, fmt.Sprintf("第 %d 卷第 %d 弧结束，触发弧级评审", volume, arc)
	}
	return false, ""
}

// WordCount counts words by rune.
func WordCount(content string) int {
	return utf8.RuneCountInString(content)
}
