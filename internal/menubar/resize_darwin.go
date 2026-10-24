//go:build menubar && darwin && cgo

package menubar

/*
#include <stdbool.h>
*/
import "C"

//export onwatchGoClampPopoverHeight
func onwatchGoClampPopoverHeight(requested C.int, maxAllowed C.int) C.int {
	return C.int(ClampPopoverHeight(int(requested), int(maxAllowed)))
}
