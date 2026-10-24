package menubar

import "testing"

func TestClampPopoverHeight(t *testing.T) {
	tests := []struct {
		name       string
		requested  int
		maxAllowed int
		want       int
	}{
		{"tiny content rises to the floor", 40, 0, MenubarPopoverMinHeight},
		{"floor boundary is inclusive", MenubarPopoverMinHeight, 0, MenubarPopoverMinHeight},
		{"inside range is unchanged", 560, 0, 560},
		{"above the constant cap is capped", 900, 0, MenubarPopoverMaxHeight},
		{"cap boundary is inclusive", MenubarPopoverMaxHeight, 0, MenubarPopoverMaxHeight},
		{"screen constraint wins when smaller", 700, 480, 480},
		{"screen constraint ignored when larger", 560, 2000, 560},
		{"non-positive screen constraint is ignored", 560, 0, 560},
		{"negative screen constraint is ignored", 560, -10, 560},
		{"nonsensical screen constraint below the floor is ignored", 560, 50, 560},
		{"negative request yields the floor", -5, 0, MenubarPopoverMinHeight},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClampPopoverHeight(tt.requested, tt.maxAllowed); got != tt.want {
				t.Errorf("ClampPopoverHeight(%d, %d) = %d, want %d", tt.requested, tt.maxAllowed, got, tt.want)
			}
		})
	}
}
