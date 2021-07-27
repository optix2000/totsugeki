# totsugeki 🐬

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
- No admin permissions needed.
- 100% transparent: Sends data bit-for-bit the same as vanilla Strive. No stat or lobby inconsistencies.

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
For example, if you live in the EU and have ~300ms ping to the GGST servers, you usually see something like `300ms * (1 TCP round trip * 2 TLS round trips + 1 HTTP Request round trip) = 1.2 seconds` per API call.

This multiplied across all 127 API calls needed to get to the main menu means it takes a whopping 152 seconds (2.5 minutes) to load into GGST.

With Totsugeki, this is brought down to a mere 38 seconds.

This has added bonus of reducing GGST server load, as TLS negotiation is one of the most CPU intensive tasks today.

Thanks to u/TarballX for doing the initial research on why GGST takes so long to connect.

## The legalese

THIS SOFTWARE IS PROVIDED AS IS AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
