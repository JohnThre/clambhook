package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/clambhook/clambhook/mobile/tunnelcore"
)

var bridgeStore = struct {
	sync.Mutex
	next     int64
	managers map[int64]*tunnelcore.Manager
}{
	next:     1,
	managers: map[int64]*tunnelcore.Manager{},
}

func main() {}

//export ClambhookTunnelCreate
func ClambhookTunnelCreate(config *C.char, errOut **C.char) C.longlong {
	setBridgeError(errOut, nil)
	mgr, err := tunnelcore.NewManager(C.GoString(config))
	if err != nil {
		setBridgeError(errOut, err)
		return 0
	}

	bridgeStore.Lock()
	handle := bridgeStore.next
	bridgeStore.next++
	bridgeStore.managers[handle] = mgr
	bridgeStore.Unlock()
	return C.longlong(handle)
}

//export ClambhookTunnelRelease
func ClambhookTunnelRelease(handle C.longlong) {
	bridgeStore.Lock()
	mgr := bridgeStore.managers[int64(handle)]
	delete(bridgeStore.managers, int64(handle))
	bridgeStore.Unlock()
	if mgr != nil {
		mgr.Stop()
	}
}

//export ClambhookTunnelStart
func ClambhookTunnelStart(handle C.longlong, errOut **C.char) C.int {
	setBridgeError(errOut, nil)
	mgr, ok := bridgeManager(handle)
	if !ok {
		setBridgeError(errOut, errors.New("tunnelcore: unknown manager handle"))
		return 0
	}
	if err := mgr.Start(); err != nil {
		setBridgeError(errOut, err)
		return 0
	}
	return 1
}

//export ClambhookTunnelStop
func ClambhookTunnelStop(handle C.longlong) {
	mgr, ok := bridgeManager(handle)
	if ok {
		mgr.Stop()
	}
}

//export ClambhookTunnelStatusJSON
func ClambhookTunnelStatusJSON(handle C.longlong, errOut **C.char) *C.char {
	setBridgeError(errOut, nil)
	mgr, ok := bridgeManager(handle)
	if !ok {
		setBridgeError(errOut, errors.New("tunnelcore: unknown manager handle"))
		return nil
	}
	return C.CString(mgr.StatusJSON())
}

//export ClambhookTunnelInjectPacket
func ClambhookTunnelInjectPacket(handle C.longlong, data unsafe.Pointer, length C.int, errOut **C.char) C.int {
	setBridgeError(errOut, nil)
	mgr, ok := bridgeManager(handle)
	if !ok {
		setBridgeError(errOut, errors.New("tunnelcore: unknown manager handle"))
		return 0
	}
	if data == nil || length <= 0 {
		setBridgeError(errOut, errors.New("tunnelcore: packet is empty"))
		return 0
	}
	packet := C.GoBytes(data, length)
	if err := mgr.InjectPacket(packet); err != nil {
		setBridgeError(errOut, err)
		return 0
	}
	return 1
}

//export ClambhookTunnelReadPacket
func ClambhookTunnelReadPacket(handle C.longlong, timeoutMillis C.int, dataOut *unsafe.Pointer, lengthOut *C.int, errOut **C.char) C.int {
	setBridgeError(errOut, nil)
	if dataOut != nil {
		*dataOut = nil
	}
	if lengthOut != nil {
		*lengthOut = 0
	}

	mgr, ok := bridgeManager(handle)
	if !ok {
		setBridgeError(errOut, errors.New("tunnelcore: unknown manager handle"))
		return -1
	}
	packet, err := mgr.ReadPacket(int(timeoutMillis))
	if errors.Is(err, tunnelcore.ErrNoPacket) {
		return 0
	}
	if err != nil {
		setBridgeError(errOut, err)
		return -1
	}
	if len(packet) == 0 {
		return 0
	}
	if dataOut == nil || lengthOut == nil {
		setBridgeError(errOut, errors.New("tunnelcore: output pointers are nil"))
		return -1
	}
	*dataOut = C.CBytes(packet)
	*lengthOut = C.int(len(packet))
	return 1
}

//export ClambhookTunnelFree
func ClambhookTunnelFree(ptr unsafe.Pointer) {
	C.free(ptr)
}

func bridgeManager(handle C.longlong) (*tunnelcore.Manager, bool) {
	bridgeStore.Lock()
	defer bridgeStore.Unlock()
	mgr, ok := bridgeStore.managers[int64(handle)]
	return mgr, ok
}

func setBridgeError(errOut **C.char, err error) {
	if errOut == nil {
		return
	}
	if err == nil {
		*errOut = nil
		return
	}
	*errOut = C.CString(err.Error())
}
