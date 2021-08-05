# Unsafe Features

These are features that can provide extra speedups, but will lose the transparency guarantee. This means we cannot guarantee that the request and response are identical to what GGST and the server would do.

These features _may_ cause GGST to behave weirdly and/or increase the chances of getting rate-limited by the server.
Please read through and understand the implications before enabling these features.

Use at your own risk.

## `-unsafe-async-stats-set`

(v1.1.0+)

Totsugeki responds to GGST instantly to any `/api/statistics/set` calls with a mocked success response. Totsugeki will forward this to the GGST servers in the background so GGST doesn't have to wait.

These calls are used for updating your R-Code.

### Speedup

10-20% speedup in the title screen compared to normal Totsugeki. R-Code updates should become instant which should speed things up in various places. You'll see a larger benefit if your upload speed is slow.

### Known/Possible issues

Since this mocks a response back to GGST, the response isn't perfect. It's unknown what parts of the response are actually used by GGST, but most seems pretty static or unused. See `HandleStatsSet()` in `proxy.go`.
It's unknown if other API's expect `/api/statistics/set` to be complete before they get called. In testing GGST didn't behave any differently, but it's still unknown if there are any other side-effects.

Can cause your R-Code updates to be lost/corrupt if you close Totsugeki before it finishes uploading your R-Code.
