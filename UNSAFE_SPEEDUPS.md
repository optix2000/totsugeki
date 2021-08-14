# Unsafe Speedups

These are speedups that can provide extra speed increases, but may cause weird bugs or break GGST. These speedups have generally been tested, but may not work for everyone.

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
