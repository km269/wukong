// Package util provides common utility functions used across the wukong project.
package util

// IntPtr returns a pointer to the given int value.
func IntPtr(v int) *int {
	return &v
}

// Float64Ptr returns a pointer to the given float64 value.
func Float64Ptr(v float64) *float64 {
	return &v
}

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(v bool) *bool {
	return &v
}

// StringPtr returns a pointer to the given string value.
func StringPtr(v string) *string {
	return &v
}
