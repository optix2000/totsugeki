# totsugeki 🐬 <a href="https://github.com/optix2000/totsugeki/actions"><img src="https://img.shields.io/github/workflow/status/optix2000/totsugeki/Builds/master" alt="Builds"></a> <a href="https://github.com/optix2000/totsugeki/releases/latest"><img alt="GitHub release" src="https://img.shields.io/github/v/release/optix2000/totsugeki"></a> <a href="https://github.com/optix2000/totsugeki/releases"><img src="https://img.shields.io/github/downloads/optix2000/totsugeki/total"></a> <a href="https://twitter.com/ggst_totsugeki"><img alt="Twitter Follow" src="https://img.shields.io/twitter/follow/ggst_totsugeki?style=social"></a>

Guilty Gear Strive Proxy for faster loading screens.

Totsugeki lets you totsugeki past the Strive connection screen.

https://user-images.githubusercontent.com/1121068/128973383-77b79b44-1998-48c6-aa67-e3014cf8f779.mp4

[Better comparison (YouTube)](https://www.youtube.com/watch?v=EsVe77QBW2Y)

## Quickstart

1. [Download](https://github.com/optix2000/totsugeki/releases/latest/download/totsugeki.exe) ([All Downloads](https://github.com/optix2000/totsugeki/releases))
2. Run `totsugeki.exe`
3. 🐬

Removing Totsugeki is as simple as deleting the executable and launching the game normally.

## Features

- 3-4x Speedup compared to vanilla Strive. 6x+ speedup with \*Unga-Bunga enabled.
- No installation or messing with system files. Just download and run.
- No administrator permissions needed.
- \*100% transparent: Sends data bit-for-bit the same as vanilla Strive. No stat or lobby inconsistencies.

\* See [Unga-Bunga/Unsafe Speedups](https://github.com/optix2000/totsugeki/blob/master/UNSAFE_SPEEDUPS.md). Unga-Bunga/Unsafe Speedups makes GGST much faster, but makes Totsugeki no longer transparent and may cause other issues.

## Advanced Usage

(Supported in v0.1.0+)

You can disable any functionality of Totsugeki by adding `-no-<feature>` as an argument to `totsugeki.exe`. For example `C:\Users\user\Downloads\totsugeki.exe -no-launch` will no longer launch GGST.

Valid options:

```none
  -help
        This help text.
  -no-launch
        Don't launch GGST. Useful if you want to launch GGST through other means.
  -no-patch
        Don't patch GGST with proxy address.
  -no-proxy
        Don't start local proxy. Useful if you want to run your own proxy.
  -no-close
        Don't automatically close totsugeki alongside GGST. (v1.0.0+)
  -no-update
        Don't automatically update totsugeki if there's a new version. (v1.4.0+)
  -unga-bunga
        Enable all unsafe speedups for maximum speed. Please read https://github.com/optix2000/totsugeki/blob/dev/UNSAFE_SPEEDUPS.md (v1.2.0+)
  -version
        Print the version number and exit.
```

The easiest way to do this would be to create a shortcut to `totsugeki.exe` and add the argument on the shortcut.

<img src="https://user-images.githubusercontent.com/1121068/127271607-8866b52b-ce69-4661-9fa2-50f00833a1aa.png" alt="Shortcut Properties" width="300">

### More Speedups (Unsafe Speedups)

Want more speed?

See `-unga-bunga` and [UNSAFE_SPEEDUPS.md](https://github.com/optix2000/totsugeki/blob/dev/UNSAFE_SPEEDUPS.md)

## Building

Tested with Golang 1.17, but probably will compile with Golang 1.13+.

### Installing from source

`go install github.com/optix2000/totsugeki`

### Building from cloned repo

`go build`

## The technical nitty gritty

GGST makes a new TCP connection and a new TLS connection _every_ API call it makes. [And it makes hundreds of them in the title screen](https://www.reddit.com/r/Guiltygear/comments/oaqwo5/analysis_of_network_traffic_at_game_startup/).

Totsugeki solves this by proxying all API requests through a keepalive connection.

What this means is instead of doing 4 round trips (TCP + TLS + HTTP) for each API call, it only needs to do 1 (HTTP only). This shortens the loading time by a factor of FOUR!
For example, if you live in the EU and have ~300ms ping to the GGST servers, you usually see something like `300ms * (1 TCP round trip + 2 TLS round trips + 1 HTTP Request round trip) = 1.2 seconds` per API call.

This multiplied across all 127 API calls needed to get to the main menu means it takes a whopping 152 seconds (2.5 minutes) to load into GGST.

With Totsugeki, this is brought down to a mere 38 seconds.

This has added bonus of reducing GGST server load, as TLS negotiation is one of the most CPU intensive tasks today.

Thanks to [u/TarballX](https://www.reddit.com/user/TarballX) for doing the [initial research](https://www.reddit.com/r/Guiltygear/comments/oaqwo5/analysis_of_network_traffic_at_game_startup/) on why GGST takes so long to connect.
