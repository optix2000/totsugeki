package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/blang/semver/v4"
	"github.com/inconshreveable/go-update"
	"github.com/optix2000/totsugeki/patcher"
	"github.com/optix2000/totsugeki/proxy"

	"golang.org/x/sys/windows"
)

//go:generate go-winres make --product-version=git-tag --file-version=git-tag

// Filled in at build time
var Version string = "(unknown version)"
var UngaBungaMode string = ""

const GGStriveExe = "GGST-Win64-Shipping.exe"

const APIOffsetAddr uintptr = 0x34D23F8 // 1.16

const PatchRetries = 30

const GGStriveAPIURL = "https://ggst-game.guiltygear.com/api/"
const PatchedAPIURL = "http://127.0.0.1:21611/api/"

const GithubDownloadURL = "https://github.com/optix2000/totsugeki/releases/latest/download/"
const GithubReleasesURL = "https://api.github.com/repos/optix2000/totsugeki/releases/latest"

const UpdateTimeout = 30 * time.Second
const UpdateName = "totsugeki.exe"
const UpdateNameUngaBunga = "totsugeki-unga-bunga.exe"

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

type Release = struct {
	TagName string `json:"tag_name"`
}

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

func cancelableSleep(ctx context.Context, delay time.Duration) {
	wait, waitCancel := context.WithTimeout(ctx, delay)
	<-wait.Done()
	waitCancel()
}

func clearScreen() {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) == nil {
		if windows.SetConsoleMode(handle, (mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)) == nil {
			windows.SetConsoleCursorPosition(handle, windows.Coord{X: 0, Y: 0})
			fmt.Printf("\x1b[2J") // Clear screen
		}
		windows.SetConsoleMode(handle, mode) // Reset console even on failure since it's technically set with an invalid setting.
	}
}

func disableQuickEdit() {
	handle, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err == nil {
		defer windows.CloseHandle(handle)
		var mode uint32
		if windows.GetConsoleMode(handle, &mode) == nil {
			windows.SetConsoleMode(handle, (mode&^windows.ENABLE_QUICK_EDIT_MODE)|windows.ENABLE_EXTENDED_FLAGS) // https://docs.microsoft.com/en-us/windows/console/setconsolemode
		}

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
						sig <- os.Interrupt // Gracefully shutdown
						return
					}

					if patchedPid != 0 {
						fmt.Println("Waiting for GGST process...")
						patchedPid = 0
					}
					cancelableSleep(ctx, 2*time.Second)
					continue
				} else {
					panic(err)
				}
			}
			if pid == patchedPid {
				cancelableSleep(ctx, 5*time.Second)
				continue
			} else if patchedPid > 1 { // Catch case where GGST restarts in between detection loop.
				continue
			}
			var retry int
			for retry = 0; retry < PatchRetries; retry++ {
				cancelableSleep(ctx, 1000*time.Millisecond) // Give GGST some time to finish loading. EnumProcessModules() doesn't like modules changing while it's running.
				var offset uintptr
				offset, err = patcher.PatchProc(pid, GGStriveExe, APIOffsetAddr, []byte(GGStriveAPIURL), []byte(PatchedAPIURL))
				if errors.Is(err, patcher.ErrOffsetMismatch) {
					fmt.Printf("WARNING: Offset found at unknown location. This version of Totsugeki has not been tested with this version of GGST and may cause issues.\n")
					err = nil
				}
				if err != nil {
					if errors.Is(err, patcher.ErrProcessAlreadyPatched) {
						fmt.Printf("GGST with PID %d is already patched at offset 0x%x.\n", pid, offset)
						if !noClose {
							close = true
						}
						break
					} else if errors.Unwrap(err) == syscall.Errno(windows.ERROR_ACCESS_DENIED) {
						messageBox("Could not patch GGST. Steam/GGST may be running as Administrator. Try re-running Totsugeki as Administrator.")
						os.Exit(1)
					} else {
						fmt.Printf("Error with PID %d at offset 0x%x: %v\n", pid, offset, err)
						continue
					}
				} else {
					fmt.Printf("Patched GGST with PID %d at offset 0x%x.\n", pid, offset)
					if !noClose {
						close = true
					}
					break
				}
			}
			if retry >= PatchRetries {
				panic(err)
			}
			patchedPid = pid
		}
	}
}

func autoUpdate() error {
	url := GithubDownloadURL + UpdateName
	if UngaBungaMode != "" {
		url = GithubDownloadURL + UpdateNameUngaBunga
	}

	client := http.Client{
		Timeout: UpdateTimeout,
	}
	resp, err := client.Get(GithubReleasesURL)

	if err != nil {
		return errors.New("could not get latest release version number")
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.New("could not read latest release version")
	}

	release := &Release{}
	err = json.Unmarshal(body, release)
	if err != nil {
		return errors.New("could not read JSON response with version number")
	}

	currentVersion, err := semver.ParseTolerant(Version)
	if err != nil {
		return errors.New("could not parse current version number")
	}

	latestVersion, err := semver.ParseTolerant(release.TagName)
	if err != nil {
		return errors.New("could not parse latest version number")
	}

	if currentVersion.LT(latestVersion) { // Only update if newer and we are running a stable version.
		if len(currentVersion.Pre) != 0 {
			if currentVersion.Pre[0].String() == "unga-bunga" {
				if currentVersion.FinalizeVersion() == latestVersion.FinalizeVersion() { // Versions are actually the same
					return nil
				}
			} else {
				fmt.Printf("New version v%v available, but cannot update from test/pre-release version v%v.\n", latestVersion, currentVersion)
				return nil
			}
		}
		exePath, err := os.Executable()
		if err != nil {
			return errors.New("could not get executable path")
		}

		fmt.Println("New version released, downloading...")
		resp, err := http.Get(url)
		if err != nil {
			return errors.New("could not download new version")
		}
		defer resp.Body.Close()

		err = update.Apply(resp.Body, update.Options{})

		if err != nil {
			return errors.New("could not update totsugeki")
		}

		clearScreen()

		command := exec.Command(exePath, os.Args[1:]...)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.Stdin = os.Stdin
		err = command.Start()

		if err != nil {
			return errors.New("could not start new totsugeki version")
		}

		os.Exit(0)
	}

	return nil
}

func main() {
	var noProxy = flag.Bool("no-proxy", false, "Don't start local proxy. Useful if you want to run your own proxy.")
	var noLaunch = flag.Bool("no-launch", false, "Don't launch GGST. Useful if you want to launch GGST through other means.")
	var noPatch = flag.Bool("no-patch", false, "Don't patch GGST with proxy address.")
	var noClose = flag.Bool("no-close", false, "Don't automatically close totsugeki alongside GGST.")
	var noUpdate = flag.Bool("no-update", false, "Don't check for totsugeki updates.")
	var unsafeAsyncStatsSet = flag.Bool("unsafe-async-stats-set", false, "UNSAFE: Asynchronously upload stats (R-Code) in the background.")
	var unsafePredictStatsGet = flag.Bool("unsafe-predict-stats-get", false, "UNSAFE: Asynchronously precache expected statistics/get calls.")
	var unsafeCacheNews = flag.Bool("unsafe-cache-news", false, "UNSAFE: Cache first news call and return cached version on subsequent calls.")
	var unsafeNoNews = flag.Bool("unsafe-no-news", false, "UNSAFE: Return an empty response for news.")
	var unsafePredictReplay = flag.Bool("unsafe-predict-replay", false, "UNSAFE: Asynchronously precache expected get_replay calls. Needs unsafe-predict-stats-get to work.")
	var unsafeCacheEnv = flag.Bool("unsafe-cache-env", false, "UNSAFE: Cache first get_env call and return cached version on subsequent calls.")
	var unsafeCacheFollow = flag.Bool("unsafe-cache-follow", false, "UNSAFE: Cache first get_follow and get_block calls and return cached version on subsequent calls.")
	var ungaBunga = flag.Bool("unga-bunga", UngaBungaMode != "", "UNSAFE: Enable all unsafe speedups for maximum speed. Please read https://github.com/optix2000/totsugeki/blob/master/UNSAFE_SPEEDUPS.md")
	var iKnowWhatImDoing = flag.Bool("i-know-what-im-doing", false, "UNSAFE: Suppress any UNSAFE warnings. I hope you know what you're doing...")
	var ver = flag.Bool("version", false, "Print the version number and exit.")

	flag.Parse()

	if *ver {
		fmt.Printf("totsugeki %v\n", Version)
		os.Exit(0)
	}

	title, err := windows.UTF16PtrFromString(fmt.Sprintf("Totsugeki %v", Version))
	if err == nil {
		procSetConsoleTitle.Call(uintptr(unsafe.Pointer(title)))
	}

	disableQuickEdit()

	fmt.Println(totsugeki)
	fmt.Printf("                                         %s\n", Version)

	// Raise an alert box on panic so non-technical users don't lose the output.
	defer func() {
		r := recover()
		if r != nil {
			panicBox(r)
		}
	}()

	if !*noUpdate && Version != "(unknown version)" {
		err := autoUpdate()
		if err != nil {
			fmt.Printf("Failed to update totsugeki: %v.\n", err.Error())
		}
	}

	if *ungaBunga { // Mash only
		*unsafeAsyncStatsSet = true
		*unsafePredictStatsGet = true
		*unsafeNoNews = true
		*unsafePredictReplay = true
		*unsafeCacheEnv = true
		*unsafeCacheFollow = true
	}

	// Drop process priority
	handle := windows.CurrentProcess()
	err = windows.SetPriorityClass(handle, windows.BELOW_NORMAL_PRIORITY_CLASS)
	if err != nil {
		fmt.Println(err)
	}
	windows.CloseHandle(handle)

	// Launch GGST if it's not already running
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

	ctx, cancel := context.WithCancel(context.Background()) // Context for graceful shutdown
	defer cancel()
	sig = make(chan os.Signal, 1)

	// Start Patcher
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
				CacheNews:       *unsafeCacheNews,
				NoNews:          *unsafeNoNews,
				PredictReplay:   *unsafePredictReplay,
				CacheEnv:        *unsafeCacheEnv,
				CacheFollow:     *unsafeCacheFollow,
			})

			fmt.Println("Started Proxy Server on port 21611.")
			err := server.Server.ListenAndServe()
			if err != nil {
				if !errors.Is(err, http.ErrServerClosed) {
					panic(err)
				}
			}
		}()

		// Watch for signal to do graceful shutdown
		wg.Add(1)
		go func() {
			defer wg.Done()
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			cancel()
			server.Shutdown()
		}()
	}

	if !*iKnowWhatImDoing && (*unsafeAsyncStatsSet || *unsafePredictStatsGet || *unsafeCacheNews || *unsafeNoNews || *unsafeCacheEnv || *unsafePredictReplay || *unsafeCacheFollow) {
		fmt.Println("WARNING: Unsafe feature used. Make sure you understand the implications: https://github.com/optix2000/totsugeki/blob/master/UNSAFE_SPEEDUPS.md")
	}

	wg.Wait()
}
