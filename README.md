# totsugeki
Guilty Gear Strive Proxy for faster loading

Totsugeki "fixes" the "Communicating with Server" issues and lets you totsugeki past the connection screen.

## Running
Running Totsugeki without Strive open will launch Strive, or you can run this any time before the main menu. This _may_ trigger antivirus software as it patches GGST live.

Removing Totsugeki is as simple as deleting the executable and restarting the game.


## The technical nitty gritty:
Strive makes a new TCP connection and a new TLS connection _every_ API call it makes. [And it makes hundreds of them in the title screen](https://www.reddit.com/r/Guiltygear/comments/oaqwo5/analysis_of_network_traffic_at_game_startup/).

Totsugeki runs a local Strive API server and patches the Strive to use the local API server. Despite the fact that Strive makes a new connection for every API call, it's moot as it takes ~0ms to hit the local API server. The local API server forwards all the API calls over a proper keepalive connection to the actual Strive servers.

What this means is instead of doing 4 round trips (TCP + TLS + HTTP) for each API call, it only needs to do 1 (HTTP only). This shortens the loading time by a factor of FOUR!
For example, if you live in the EU and have ~300ms ping to the Strive servers, you usually see something like 300ms * (1 TCP round trip * 2 TLS round trips + 1 HTTP Request round trip) = 1.2 seconds per API call.

This multiplied across all 127 API calls needed to get to the main menu means it takes a whopping 152 seconds (2.5 minutes) to load into Strive.

With Totsugeki, this is brought down to a mere 38 seconds.

This should have the added bonus of reducing Strive server load, as TLS negotiation is one of the most CPU intensive tasks today.

Thanks to u/TarballX for doing the initial research on why Strive takes so long to connect.

I plan on adding some caching and pre-sending requests to further speed up loading.

## Building
Tested with Golang 1.16, but probably can compile with Golang 1.13.

### Installing from source
`go install github.com/optix2000/totsugeki`

### Building from cloned repo
`go build`

### The legalese

THIS SOFTWARE IS PROVIDED AS IS AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL I BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
