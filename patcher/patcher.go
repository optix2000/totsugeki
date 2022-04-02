package patcher

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Errors
var ErrProcessAlreadyPatched = errors.New("process already patched")
var ErrProcessNotFound = errors.New("couldn't find process")
var ErrAPINotFound = errors.New("couldn't find API address in memory")
var ErrOffsetMismatch = errors.New("offset found at different location")

func min(a uint32, b uint32) uint32 {
	if a > b {
		return b
	}
	return a
}

func max(a uint32, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func SearchMemory(proc windows.Handle, LPBaseOfDll uintptr, SizeOfImage uint32, value []byte, altvalue []byte) (uintptr, error) {
	// Information about the contents of the memory to read
	var memoryBasicInfo windows.MemoryBasicInformation

	var p uintptr = LPBaseOfDll
	var offset uintptr
	var MAX_CHUNK_SIZE uintptr = 4096 // Just an abitrary size
	chunk := make([]byte, MAX_CHUNK_SIZE)
	// Programmatically find the offset of the API url
	// Don't search beyond the end of the application memory
	for p < LPBaseOfDll+uintptr(SizeOfImage) {
		err := windows.VirtualQueryEx(proc, p, &memoryBasicInfo, unsafe.Sizeof(memoryBasicInfo))
		if err != nil {
			return 0, fmt.Errorf("error in VirtualQueryEx: %w", err)
		}

		var bytesRead uintptr
		var chunkIndex uintptr
		// chunkRollover moves the last bit of the chunk to the beginning of the next chunk
		// This way, if the API is across 2 chunk, it will be caught. Basically an easy but inefficient circular buffer
		// The size of the chunkRollover is the size of the biggest thing we are looking for
		// The longer the string we are looking for, the less efficient the search is, could look at making the chunk bigger to offset
		var chunkRollover = uintptr(max(uint32(unsafe.Sizeof(value)), uint32(unsafe.Sizeof(altvalue))))

		for chunkIndex < memoryBasicInfo.RegionSize {
			// Read the chunk of memory into a byte array, It doesn't matter if we ask for more data than the size of the region
			err = windows.ReadProcessMemory(proc, memoryBasicInfo.BaseAddress+chunkIndex, &chunk[chunkRollover], MAX_CHUNK_SIZE-chunkRollover, &bytesRead)
			if err != nil {
				return 0, fmt.Errorf("error in ReadProcessMemory: %w", err)
			}

			// See if the chunk contains the API url and get its offset if its there
			// Only gets the first instance of the API url in memory
			var ind = bytes.Index(chunk[chunkRollover:bytesRead], value)
			if ind != -1 {
				// Override offset with the found API url index
				offset = (p + chunkIndex + uintptr(ind))
				return offset - LPBaseOfDll, nil
			}
			// If the chunk doesn't have the API address, check if its already been patched?
			ind = bytes.Index(chunk[chunkRollover:bytesRead], altvalue)
			if ind != -1 {
				offset = p + chunkIndex + uintptr(ind)
				return offset - LPBaseOfDll, ErrProcessAlreadyPatched
			}

			chunkIndex = chunkIndex + bytesRead
			// Move the last bytes of the chunk to the beginning
			rollover := chunk[bytesRead-chunkRollover : bytesRead]
			chunk = append(rollover, chunk...)
			// Cut off the size of the chunk so it doesn't keep growing
			chunk = chunk[:MAX_CHUNK_SIZE]
		}
		// If not found in this region, try the next region
		p = p + memoryBasicInfo.RegionSize
	}

	return 0, ErrAPINotFound
}

func VerifyAPIPatch(proc windows.Handle, addr uintptr, old []byte, new []byte) error {
	var buf = make([]byte, len(old))
	var bytesRead uintptr

	// Verify we're at the correct offset
	err := windows.ReadProcessMemory(proc, addr, &buf[0], uintptr(len(old)), &bytesRead)
	if err != nil {
		return fmt.Errorf("error in ReadProcessMemory: %w", err)
	}

	if !bytes.Equal(buf[:bytesRead], old) {
		if bytes.Equal(buf[:min(uint32(bytesRead), uint32(len(new)))], new) {
			return ErrProcessAlreadyPatched
		}
		return fmt.Errorf("%q does not match signature at offset 0x%x", buf[:bytesRead], addr)
	}

	return nil
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

func PatchProc(pid uint32, moduleName string, offsetAddr uintptr, old []byte, new []byte) (uintptr, error) {
	proc, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_VM_WRITE|windows.PROCESS_VM_OPERATION|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("error in OpenProcess: %w", err)
	}
	defer windows.CloseHandle(proc)

	var modules [512]windows.Handle // TODO: Don't hardcode
	var cb = uint32(unsafe.Sizeof(modules))
	var cbNeeded uint32

	err = windows.EnumProcessModules(proc, &modules[0], cb, &cbNeeded)
	if err != nil && err != windows.ERROR_PARTIAL_COPY { // Partial copies are fine
		return 0, fmt.Errorf("error in EnumProcessModules: %w", err)
	}

	// Look for base module
	var i uint32
	var module windows.Handle
	for i = 0; i < cbNeeded/uint32(unsafe.Sizeof(modules[0])); i++ {
		var moduleNameBuf [260]uint16 // TODO: Don't hardcode
		err = windows.GetModuleFileNameEx(proc, modules[i], &moduleNameBuf[0], uint32(len(moduleNameBuf)))
		if err != nil {
			return 0, fmt.Errorf("error in GetModuleFileNameExA: %w", err)
		}

		if strings.EqualFold(filepath.Base(strings.TrimRight(windows.UTF16ToString(moduleNameBuf[:]), "\000")), moduleName) {
			module = modules[i]
			break
		}
	}
	if module == 0 {
		return 0, fmt.Errorf("couldn't find base module for %v", moduleName)
	}

	// Get Entrypoint so we have an idea where GGST's memory starts
	var moduleInfo = windows.ModuleInfo{}

	cb = uint32(unsafe.Sizeof(moduleInfo))

	err = windows.GetModuleInformation(proc, module, &moduleInfo, cb)
	if err != nil { // err is always set, even on success. Need to look at return value
		return 0, fmt.Errorf("error in GetModuleInformationCall: %w", err)
	}

	var addr = moduleInfo.BaseOfDll + offsetAddr

	// Check if the API is at the offset specified
	err = VerifyAPIPatch(proc, addr, old, new)
	if err != nil {
		// If the offset doesn't have the old or new API address, try searching memory
		offsetAddr, err = SearchMemory(proc, moduleInfo.BaseOfDll, moduleInfo.SizeOfImage, old, new)
		if err != nil {
			return offsetAddr, err
		}
		addr = moduleInfo.BaseOfDll + offsetAddr
	}

	// Set memory writable
	var oldProtect uint32
	err = windows.VirtualProtectEx(proc, addr, uintptr(len(old)), windows.PAGE_READWRITE, &oldProtect)
	if err != nil {
		return addr, fmt.Errorf("error in VirtualProtectEx: %w", err)
	}

	var bytesWritten uintptr
	buf := make([]byte, len(old))
	copy(buf, new)
	err = windows.WriteProcessMemory(proc, addr, &buf[0], uintptr(len(old)), &bytesWritten)
	if err != nil {
		return addr, fmt.Errorf("error in WriteProcessMemory: %w", err)
	}

	// re-protect memory after patching
	err = windows.VirtualProtectEx(proc, addr, uintptr(len(old)), oldProtect, &oldProtect)
	if err != nil {
		return addr, fmt.Errorf("error in VirtualProtectEx: %w", err)
	}

	err = nil
	if offsetAddr != (addr - moduleInfo.BaseOfDll) {
		err = ErrOffsetMismatch
	}
	return offsetAddr, err
}
