package patcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Other necessary Windows API's
var modKernel32 *windows.LazyDLL = windows.NewLazySystemDLL("kernel32.dll")
var modPSAPI *windows.LazyDLL = windows.NewLazySystemDLL("psapi.dll")
var procReadProcessMemory *windows.LazyProc = modKernel32.NewProc("ReadProcessMemory")
var procWriteProcessMemory *windows.LazyProc = modKernel32.NewProc("WriteProcessMemory")
var procVirtualProtectEx *windows.LazyProc = modKernel32.NewProc("VirtualProtectEx")
var procVirtualQueryEx *windows.LazyProc = modKernel32.NewProc("VirtualQueryEx")
var procEnumProcessModules *windows.LazyProc = modPSAPI.NewProc("EnumProcessModules")
var procGetModuleInformation *windows.LazyProc = modPSAPI.NewProc("GetModuleInformation")
var procGetModuleFileNameExA *windows.LazyProc = modPSAPI.NewProc("GetModuleFileNameExA")

// Errors
var ErrProcessAlreadyPatched = errors.New("process already patched")
var ErrProcessNotFound = errors.New("couldn't find process")

func min(a uint32, b uint32) uint32 {
	if a > b {
		return b
	}
	return a
}

func GetProc(proc string) (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, fmt.Errorf("error in CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	var pe32 windows.ProcessEntry32

	pe32.Size = uint32(unsafe.Sizeof(pe32)) // NB: https://docs.microsoft.com/en-us/windows/win32/api/tlhelp32/ns-tlhelp32-processentry32

	if err = windows.Process32First(snapshot, &pe32); err != nil {
		return 0, fmt.Errorf("error in Process32First: %w", err)
	}

	for {
		procName := windows.UTF16ToString(pe32.ExeFile[:]) // Windows strings are UTF-16
		if procName == proc {
			return pe32.ProcessID, nil
		}
		err = windows.Process32Next(snapshot, &pe32)
		if err != nil {
			if winErr, ok := err.(syscall.Errno); ok {
				if winErr == windows.ERROR_NO_MORE_FILES {
					break
				}
			}
			return 0, fmt.Errorf("error in Process32Next: %w", err)
		}
	}
	return 0, ErrProcessNotFound
}

func PatchProc(pid uint32, moduleName string, offsetAddr uintptr, old string, new string) (uintptr, error) {
	proc, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_VM_WRITE|windows.PROCESS_VM_OPERATION|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("error in OpenProcess: %w", err)
	}
	defer windows.CloseHandle(proc)

	var modules [512]uintptr // TODO: Don't hardcode
	var cb = uint32(unsafe.Sizeof(modules))
	var cbNeeded uint32

	ret, _, err := procEnumProcessModules.Call(uintptr(proc), uintptr(unsafe.Pointer(&modules)), uintptr(cb), uintptr(unsafe.Pointer(&cbNeeded)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		return 0, fmt.Errorf("error in EnumProcessModules: %w", err)
	}

	// Look for base module
	var i uint32
	var module uintptr
	for i = 0; i < cbNeeded/uint32(unsafe.Sizeof(modules[0])); i++ {
		var moduleNameBuf [260]byte // TODO: Don't hardcode
		ret, _, err = procGetModuleFileNameExA.Call(uintptr(proc), uintptr(modules[i]), uintptr(unsafe.Pointer(&moduleNameBuf)), unsafe.Sizeof(moduleNameBuf))
		if ret == 0 { // err is always set, even on success. Need to look at return value
			return 0, fmt.Errorf("error in GetModuleFileNameExA: %w", err)
		}
		if strings.EqualFold(filepath.Base(strings.TrimRight(string(moduleNameBuf[:]), "\000")), moduleName) {
			module = modules[i]
			break
		}
	}
	if module == 0 {
		// TODO: Better error handling
		return 0, errors.New("couldn't find base module for GGST")
	}

	// Get Entrypoint so we have an idea where GGST's memory starts
	var moduleInfo struct {
		LPBaseOfDll uintptr
		SizeOfImage uint32
		EntryPoint  uintptr
	}

	cb = uint32(unsafe.Sizeof(moduleInfo))

	ret, _, err = procGetModuleInformation.Call(uintptr(proc), module, uintptr(unsafe.Pointer(&moduleInfo)), uintptr(cb))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		return 0, fmt.Errorf("error in GetModuleInformationCall: %w", err)
	}

	var offset = moduleInfo.LPBaseOfDll + offsetAddr

	var buf = make([]byte, len(old))
	var bytesRead uint32

	// Verify we're at the correct offset
	ret, _, err = procReadProcessMemory.Call(uintptr(proc), offset, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(old)), uintptr(unsafe.Pointer(&bytesRead)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		return offset, fmt.Errorf("error in ReadProcessMemory: %w", err)
	}

	if string(buf[:bytesRead]) != old {
		if string(buf[:min(bytesRead, uint32(len(new)))]) == new {
			return offset, ErrProcessAlreadyPatched
		}
		return offset, fmt.Errorf("%q does not match signature at offset 0x%x", buf[:bytesRead], offset)
	}

	// Set memory writable
	var oldProtect uint32
	ret, _, err = procVirtualProtectEx.Call(uintptr(proc), offset, uintptr(len(old)), windows.PAGE_READWRITE, uintptr(unsafe.Pointer(&oldProtect)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		return offset, fmt.Errorf("error in VirtualProtectEx: %w", err)
	}

	var bytesWritten uint32
	buf = make([]byte, len(old))
	copy(buf, new)
	ret, _, err = procWriteProcessMemory.Call(uintptr(proc), offset, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(old)), uintptr(unsafe.Pointer(&bytesWritten)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		return offset, fmt.Errorf("error in WriteProcessMemory: %w", err)
	}

	// re-protect memory after patching
	ret, _, err = procVirtualProtectEx.Call(uintptr(proc), offset, uintptr(len(old)), uintptr(oldProtect), uintptr(unsafe.Pointer(&oldProtect)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		return offset, fmt.Errorf("error in VirtualProtectEx: %w", err)
	}

	return offset, nil
}
