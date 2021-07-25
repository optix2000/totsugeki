package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/sys/windows"
)

const GGStriveExe = "GGST-Win64-Shipping.exe"

const APIOffsetAddr uintptr = 0x3429EB0

const GGStriveAPIURL = "https://ggst-game.guiltygear.com/api/"
const PatchedAPIURL = "http://localhost:21611/api/"

// Other necessary Windows API's
var modKernel32 *windows.LazyDLL
var modPSAPI *windows.LazyDLL
var procReadProcessMemory *windows.LazyProc
var procWriteProcessMemory *windows.LazyProc
var procVirtualProtectEx *windows.LazyProc
var procVirtualQueryEx *windows.LazyProc
var procGetModuleInformation *windows.LazyProc
var procEnumProcessModules *windows.LazyProc
var procGetModuleFileNameExA *windows.LazyProc

// TODO: use this to dump debugging info
type WinAPIError struct {
	Err error
}

func (e *WinAPIError) Error() string {
	return e.Err.Error()
}

var ErrProcessAlreadyPatched = errors.New("process already patched")

var ErrProcessNotFound = errors.New("couldn't find GGST process")

func min(a uint32, b uint32) uint32 {
	if a > b {
		return b
	}
	return a
}

const totsugeki = " _____       _                             _     _ \n" +
	"|_   _|___  | |_  ___  _   _   __ _   ___ | | __(_)\n" +
	"  | | / _ \\ | __|/ __|| | | | / _` | / _ \\| |/ /| |\n" +
	"  | || (_) || |_ \\__ \\| |_| || (_| ||  __/|   < | |\n" +
	"  |_| \\___/  \\__||___/ \\__,_| \\__, | \\___||_|\\_\\|_|\n" +
	"                              |___/                "

func getGGST() (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		fmt.Println(err)
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	var pe32 windows.ProcessEntry32

	pe32.Size = uint32(unsafe.Sizeof(pe32)) // NB: https://docs.microsoft.com/en-us/windows/win32/api/tlhelp32/ns-tlhelp32-processentry32

	if err = windows.Process32First(snapshot, &pe32); err != nil {
		fmt.Println(err)
		return 0, err
	}

	for {
		procName := windows.UTF16ToString(pe32.ExeFile[:]) // Windows strings are UTF-16
		if procName == GGStriveExe {
			return pe32.ProcessID, nil
		}
		err = windows.Process32Next(snapshot, &pe32)
		if err != nil {
			if winErr, ok := err.(syscall.Errno); ok {
				if winErr == windows.ERROR_NO_MORE_FILES {
					break
				}
			}
			fmt.Println(err)
			return 0, err
		}
	}
	return 0, ErrProcessNotFound
}

func patchGGST(pid uint32) error {
	proc, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_VM_WRITE|windows.PROCESS_VM_OPERATION|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		panic(err)
	}
	defer windows.CloseHandle(proc)

	var modules [512]uintptr // TODO: Don't hardcode
	var cb = uint32(unsafe.Sizeof(modules))
	var cbNeeded uint32

	ret, _, err := procEnumProcessModules.Call(uintptr(proc), uintptr(unsafe.Pointer(&modules)), uintptr(cb), uintptr(unsafe.Pointer(&cbNeeded)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		fmt.Println(err)
		return err
	}

	// Look for base module
	var i uint32
	var module uintptr
	for i = 0; i < cbNeeded/uint32(unsafe.Sizeof(modules[0])); i++ {
		var moduleName [260]byte // TODO: Don't hardcode
		ret, _, err = procGetModuleFileNameExA.Call(uintptr(proc), uintptr(modules[i]), uintptr(unsafe.Pointer(&moduleName)), unsafe.Sizeof(moduleName))
		if ret == 0 { // err is always set, even on success. Need to look at return value
			fmt.Println(err)
			return err
		}
		if filepath.Base(string(moduleName[:])) == GGStriveExe {
			module = modules[i]
			break
		}
	}
	if module == 0 {
		// TODO: Handle errors better
		panic("Couldn't find base module for GGST")
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
		fmt.Println(err)
		return err
	}

	// Get base address so we can add our offset
	var memoryInfo struct {
		BaseAddress       uintptr
		AllocationBase    uintptr
		AllocationProtect uint32
		PartitionID       uint16
		RegionSize        uintptr
		State             uint32
		Protect           uint32
		Type              uint32
	}
	cb = uint32(unsafe.Sizeof(memoryInfo))

	ret, _, err = procVirtualQueryEx.Call(uintptr(proc), moduleInfo.EntryPoint, uintptr(unsafe.Pointer(&memoryInfo)), uintptr(cb))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		fmt.Println(err)
		return err
	}
	var offset = memoryInfo.AllocationBase + APIOffsetAddr

	fmt.Printf("Patching GGST with PID %d at offset 0x%x.\n", pid, offset)

	var buf = make([]byte, len(GGStriveAPIURL))
	var bytesRead uint32

	// Verify we're at the correct offset
	ret, _, err = procReadProcessMemory.Call(uintptr(proc), offset, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(GGStriveAPIURL)), uintptr(unsafe.Pointer(&bytesRead)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		fmt.Println(err)
		return err
	}

	if string(buf[:bytesRead]) != GGStriveAPIURL {
		if string(buf[:min(bytesRead, uint32(len(PatchedAPIURL)))]) == PatchedAPIURL {
			return ErrProcessAlreadyPatched
		}
		return fmt.Errorf("%q does not match signature at offset 0x%x", buf[:bytesRead], offset)
	}

	// Set memory writable
	var oldProtect uint32
	ret, _, err = procVirtualProtectEx.Call(uintptr(proc), offset, uintptr(len(GGStriveAPIURL)), windows.PAGE_READWRITE, uintptr(unsafe.Pointer(&oldProtect)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		fmt.Println(err)
		return err
	}

	var bytesWritten uint32
	buf = make([]byte, len(GGStriveAPIURL))
	copy(buf, PatchedAPIURL)
	ret, _, err = procWriteProcessMemory.Call(uintptr(proc), offset, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(GGStriveAPIURL)), uintptr(unsafe.Pointer(&bytesWritten)))
	if ret == 0 { // err is always set, even on success. Need to look at return value
		fmt.Println(err)
		return err
	}

	return nil
}

type StriveAPIProxy struct {
	client *http.Client
}

func (s *StriveAPIProxy) proxyRequest(r *http.Request) (*http.Response, error) {
	apiURL, err := url.Parse(GGStriveAPIURL) // TODO: Const this
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	apiURL.Path = r.URL.Path

	r.URL = apiURL
	r.Host = ""
	r.RequestURI = ""
	return s.client.Do(r)
}

// Proxy everything else
func (s *StriveAPIProxy) handleCatchall(w http.ResponseWriter, r *http.Request) {
	resp, err := s.proxyRequest(r)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	reader := io.TeeReader(resp.Body, w) // For dumping API payloads
	_, err = io.ReadAll(reader)
	if err != nil {
		fmt.Println(err)
	}
}

// GGST uses the URL from this API after initial launch so we need to intercept this.
func (s *StriveAPIProxy) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	resp, err := s.proxyRequest(r)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
	}
	buf = bytes.Replace(buf, []byte(GGStriveAPIURL), []byte(PatchedAPIURL), -1)
	w.Write(buf)
}

// Patch GGST as it starts
func watchGGST() {
	var patchedPid uint32 = 1
	for {
		pid, err := getGGST()
		if err != nil {
			if errors.Is(err, ErrProcessNotFound) {
				if patchedPid != 0 {
					fmt.Println("Waiting for GGST process...")
					patchedPid = 0
				}

				time.Sleep(2 * time.Second)
				continue
			} else {
				panic(err)
			}
		}
		if pid == patchedPid {
			time.Sleep(10 * time.Second)
			continue
		}
		err = patchGGST(pid)
		if err != nil {
			if errors.Is(err, ErrProcessAlreadyPatched) {
				fmt.Printf("GGST with PID %d is already patched.\n", pid)
			} else {
				panic(err)
			}
		} else {
			fmt.Printf("Patched GGST with PID %d.\n", pid)
		}
		patchedPid = pid
	}
}

func main() {
	// Following Windows API's are not implemented in Golang stdlib and need to be loaded from DLL manually
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modPSAPI = windows.NewLazySystemDLL("psapi.dll")
	procReadProcessMemory = modKernel32.NewProc("ReadProcessMemory")
	procWriteProcessMemory = modKernel32.NewProc("WriteProcessMemory")
	procVirtualProtectEx = modKernel32.NewProc("VirtualProtectEx")
	procVirtualQueryEx = modKernel32.NewProc("VirtualQueryEx")
	procEnumProcessModules = modPSAPI.NewProc("EnumProcessModules")
	procGetModuleInformation = modPSAPI.NewProc("GetModuleInformation")
	procGetModuleFileNameExA = modPSAPI.NewProc("GetModuleFileNameExA")

	fmt.Println(totsugeki)
	fmt.Printf("                                         %s\n", "Beta")

	_, err := getGGST()
	if err != nil {
		if errors.Is(err, ErrProcessNotFound) {
			fmt.Println("Starting GGST...")
			cmd := exec.Cmd{Path: "C:\\Program Files (x86)\\Steam\\Steam.exe", Args: []string{"steam://rungameid/1384160"}}
			err = cmd.Start()
			if err != nil {
				fmt.Println(err)
			}
		} else {
			panic(err)
		}
	}
	go watchGGST()

	// Proxy side

	proxy := &StriveAPIProxy{client: &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 1,
			MaxConnsPerHost:     2, // Don't try to flood the server with too many connections
		},
	}}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Route("/api", func(r chi.Router) {
		r.HandleFunc("/sys/get_env", proxy.handleGetEnv)
		r.HandleFunc("/*", proxy.handleCatchall)

	})

	fmt.Println("Started Proxy Server on port 21611.")
	http.ListenAndServe(":21611", r)
}

// TODO: Configurable options for everything
// TODO: Caching for most APIs (may need API caching/parsing/reversing)
