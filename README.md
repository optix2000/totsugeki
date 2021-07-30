# totsugeki 🐬 ![Build](https://github.com/optix2000/totsugeki/actions/workflows/build.yml/badge.svg)

Guilty Gear Strive Proxy for faster loading screens.

Totsugeki lets you totsugeki past the Strive connection screen.

https://user-images.githubusercontent.com/1121068/126918454-f2b2366f-2c82-4f97-acea-a36aa92485e5.mp4

## Quickstart

1. [Download](https://github.com/optix2000/totsugeki/releases)
2. Run `totsugeki.exe`
3. 🐬

Removing Totsugeki is as simple as deleting the executable and launching the game normally.

## Features

- 3-4x Speedup compared to vanilla Strive.
- No installation or messing with system files. Just download and run.
- No administrator permissions needed.
- 100% transparent: Sends data bit-for-bit the same as vanilla Strive. No stat or lobby inconsistencies.

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
  -version
        Print the version number and exit.
```

The easiest way to do this would be to create a shortcut to `totsugeki.exe` and add the argument on the shortcut.

![https://user-images.githubusercontent.com/1121068/127271607-8866b52b-ce69-4661-9fa2-50f00833a1aa.png](https://user-images.githubusercontent.com/1121068/127271607-8866b52b-ce69-4661-9fa2-50f00833a1aa.png)

## Building

Tested with Golang 1.16, but probably will compile with Golang 1.13+.

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

Thanks to u/TarballX for doing the initial research on why GGST takes so long to connect.

