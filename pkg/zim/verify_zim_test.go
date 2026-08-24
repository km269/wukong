package zim

import (
	"testing"
)

func TestVerifyZIMFile(t *testing.T) {
	zimPath := "e:\\myVibeCoding\\km269\\wukong\\.wukong\\apps\\cloned\\www.state.gov.zim"
	err := VerifyZIMFile(zimPath)
	if err != nil {
		t.Fatalf("VerifyZIMFile failed: %v", err)
	}
}
