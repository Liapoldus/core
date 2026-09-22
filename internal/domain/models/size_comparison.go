package models

type SizeComparison struct {
	GreaterThan        *uint64
	GreaterThanOrEqual *uint64
	LessThan           *uint64
	LessThanOrEqual    *uint64
}

func (comparison SizeComparison) Matches(value uint64) bool {
	if comparison.GreaterThan != nil && value <= *comparison.GreaterThan {
		return false
	}
	if comparison.GreaterThanOrEqual != nil && value < *comparison.GreaterThanOrEqual {
		return false
	}
	if comparison.LessThan != nil && value >= *comparison.LessThan {
		return false
	}
	if comparison.LessThanOrEqual != nil && value > *comparison.LessThanOrEqual {
		return false
	}
	return true
}
