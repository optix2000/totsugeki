package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/optix2000/totsugeki/patcher"
	"github.com/optix2000/totsugeki/proxy"

	"golang.org/x/sys/windows"
)

//go:generate go-winres make --product-version=git-tag --file-version=git-tag

var Version string = "(unknown version)"
var UngaBungaMode string = ""

const GGStriveExe = "GGST-Win64-Shipping.exe"

const APIOffsetAddr uintptr = 0x342AD10 // 1.07

const GGStriveAPIURL = "https://ggst-game.guiltygear.com/api/"
const PatchedAPIURL = "http://127.0.0.1:21611/api/"

const totsugeki = " _____       _                             _     _ \n" +
	"|_   _|___  | |_  ___  _   _   __ _   ___ | | __(_)\n" +
	"  | | / _ \\ | __|/ __|| | | | / _` | / _ \\| |/ /| |\n" +
	"  | || (_) || |_ \\__ \\| |_| || (_| ||  __/|   < | |\n" +
	"  |_| \\___/  \\__||___/ \\__,_| \\__, | \\___||_|\\_\\|_|\n" +
	"                              |___/                "

var server *proxy.StriveAPIProxy
var sig chan os.Signal

var modKernel32 *windows.LazyDLL = windows.NewLazySystemDLL("kernel32.dll")
var procSetConsoleTitle *windows.LazyProc = modKernel32.NewProc("SetConsoleTitleW")

func panicBox(v interface{}) {
	const header = `Totsugeki has encountered a fatal error.

Please report this to https://github.com/optix2000/totsugeki/issues

===================

Error: %v

%v`
	messageBox(fmt.Sprintf(header, v, string(debug.Stack())))

	panic(v)
}

func messageBox(message string) {
	msg, e := windows.UTF16PtrFromString(message)
	if e != nil {
		fmt.Println(e)
		panic(e)
	}
	_, e = windows.MessageBox(0, msg, nil, windows.MB_OK|windows.MB_ICONWARNING|windows.MB_SETFOREGROUND)
	if e != nil {
		fmt.Println(e)
		panic(e)
	}
}

// Patch GGST as it starts
func watchGGST(noClose bool, ctx context.Context) {
	var patchedPid uint32 = 1
	var close bool = false

	for {

		select {
		case <-ctx.Done():
			return
		default:
			pid, err := patcher.GetProc(GGStriveExe)
			if err != nil {
				if errors.Is(err, patcher.ErrProcessNotFound) {
					if close {
						sig <- os.Interrupt
						return
					}

					if patchedPid != 0 {
						fmt.Println("Waiting for GGST process...")
						patchedPid = 0
					}
					fastSleep, fastCancel := context.WithTimeout(ctx, 2*time.Second)
					<-fastSleep.Done()
					fastCancel()
					continue
				} else {
					panic(err)
				}
			}
			if pid == patchedPid {
				slowSleep, slowCancel := context.WithTimeout(ctx, 10*time.Second)
				<-slowSleep.Done()
				slowCancel()
				continue
			}
			time.Sleep(100 * time.Millisecond) // Give GGST some time to finish loading. EnumProcessModules() doesn't like modules changing while it's running.
			offset, err := patcher.PatchProc(pid, GGStriveExe, APIOffsetAddr, []byte(GGStriveAPIURL), []byte(PatchedAPIURL))
			if err != nil {
				if errors.Is(err, patcher.ErrProcessAlreadyPatched) {
					fmt.Printf("GGST with PID %d is already patched at offset 0x%x.\n", pid, offset)
					if !noClose {
						close = true
					}
				} else if errors.Unwrap(err) == syscall.Errno(windows.ERROR_ACCESS_DENIED) {
					messageBox("Could not patch GGST. Steam/GGST may be running as Administrator. Try re-running Totsugeki as Administrator.")
					os.Exit(1)
				} else {
					fmt.Printf("Error at offset 0x%x: %v", offset, err)
					panic(err)
				}
			} else {
				fmt.Printf("Patched GGST with PID %d at offset 0x%x.\n", pid, offset)
				if !noClose {
					close = true
				}
			}
			patchedPid = pid
		}
	}
}

func main() {
	var noProxy = flag.Bool("no-proxy", false, "Don't start local proxy. Useful if you want to run your own proxy.")
	var noLaunch = flag.Bool("no-launch", false, "Don't launch GGST. Useful if you want to launch GGST through other means.")
	var noPatch = flag.Bool("no-patch", false, "Don't patch GGST with proxy address.")
	var noClose = flag.Bool("no-close", false, "Don't automatically close totsugeki alongside GGST.")
	var unsafeAsyncStatsSet = flag.Bool("unsafe-async-stats-set", false, "UNSAFE: Asynchronously upload stats (R-Code) in the background.")
	var unsafePredictStatsGet = flag.Bool("unsafe-predict-stats-get", false, "UNSAFE: Asynchronously precache expected statistics/get calls.")
	var ungaBunga = flag.Bool("unga-bunga", UngaBungaMode != "", "Enable all unsafe speedups for maximum speed. Please read https://github.com/optix2000/totsugeki/blob/dev/UNSAFE_SPEEDUPS.md")
	var iKnowWhatImDoing = flag.Bool("i-know-what-im-doing", false, "UNSAFE: Suppress any UNSAFE warnings. I hope you know what you're doing...")
	var ver = flag.Bool("version", false, "Print the version number and exit.")

	flag.Parse()

	if *ver {
		fmt.Printf("totsugeki %v", Version)
		os.Exit(0)
	}

	title, err := windows.UTF16PtrFromString(fmt.Sprintf("Totsugeki %s", Version))
	if err == nil {
		procSetConsoleTitle.Call(uintptr(unsafe.Pointer(title)))
	}
	fmt.Println(totsugeki)
	fmt.Printf("                                         %s\n", Version)

	// Raise an alert box on panic so non-technical users don't lose the output.
	defer func() {
		r := recover()
		if r != nil {
			panicBox(r)
		}
	}()

	if *ungaBunga { // Mash only
		*unsafeAsyncStatsSet = true
		*unsafePredictStatsGet = true
	}

	handle := windows.CurrentProcess()
	err = windows.SetPriorityClass(handle, windows.BELOW_NORMAL_PRIORITY_CLASS)
	if err != nil {
		fmt.Println(err)
	}
	windows.CloseHandle(handle)

	if !*noLaunch {
		_, err := patcher.GetProc(GGStriveExe)
		if err != nil {
			if errors.Is(err, patcher.ErrProcessNotFound) {
				fmt.Println("Starting GGST...")
				err = exec.Command("rundll32", "url.dll,FileProtocolHandler", "steam://rungameid/1384160").Start()
				if err != nil {
					fmt.Println(err)
				}
			} else {
				panic(err)
			}
		}
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig = make(chan os.Signal, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
		server.Shutdown()
	}()

	if !*noPatch {
		wg.Add(1)
		go func() {
			// Raise an alert box on panic so non-technical users don't lose the output.
			defer func() {
				r := recover()
				if r != nil {
					panicBox(r)
				}
			}()
			defer wg.Done()
			watchGGST(*noClose, ctx)
		}()
	}

	// Proxy side
	if !*noProxy {
		wg.Add(1)
		go func() {
			// Raise an alert box on panic so non-technical users don't lose the output.
			defer func() {
				r := recover()
				if r != nil {
					panicBox(r)
				}
			}()
			defer wg.Done()

			server = proxy.CreateStriveProxy("127.0.0.1:21611", GGStriveAPIURL, PatchedAPIURL, &proxy.StriveAPIProxyOptions{
				AsyncStatsSet:   *unsafeAsyncStatsSet,
				PredictStatsGet: *unsafePredictStatsGet,
			})

			fmt.Println("Started Proxy Server on port 21611.")
			err := server.Server.ListenAndServe()
			if err != nil {
				if !errors.Is(err, http.ErrServerClosed) {
					panic(err)
				}
			}
		}()
	}

	if !*iKnowWhatImDoing && (*unsafeAsyncStatsSet || *unsafePredictStatsGet) {
		fmt.Println("WARNING: Unsafe feature used. Make sure you understand the implications: https://github.com/optix2000/totsugeki/blob/master/UNSAFE_FEATURES.md")
	}

	wg.Wait()
}

// TODO: Caching for most APIs (may need API caching/parsing/reversing)
