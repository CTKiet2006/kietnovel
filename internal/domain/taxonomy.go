package domain

import "slices"

var (
	hookTypes       = []string{"crisis", "mystery", "desire", "emotion", "choice"}
	dominantStrands = []string{"quest", "fire", "constellation"}
)

// HookTypes 返回章节钩子分类。返回副本，调用方不能修改领域词表。
func HookTypes() []string { return slices.Clone(hookTypes) }

// DominantStrands 返回章节主导叙事线分类。
func DominantStrands() []string { return slices.Clone(dominantStrands) }

func ValidHookType(value string) bool       { return slices.Contains(hookTypes, value) }
func ValidDominantStrand(value string) bool { return slices.Contains(dominantStrands, value) }
