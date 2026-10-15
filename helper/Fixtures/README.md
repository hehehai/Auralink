# Helper protocol fixtures

Golden transcripts of the helper protocol (`docs/helper-protocol.md`), one JSON object per line:

```json
{"to":"helper","msg":{…}}   // app → helper (a request)
{"to":"app","msg":{…}}      // helper → app (a response or a notification)
```

The format is AudioSmith's engine fixtures', with the helper in the engine's place.

`go test ./internal/helper/proto` decodes every line strictly with the Go types (unknown fields fail), checks that it encodes back to the same JSON, and checks the sequencing rules: one response per request, a job's notifications before its response, `job.cancel` and `capture.stop` answered after the job they end. `TestFixturesCoverMethods` fails when a method has no fixture. The Swift helper's tests will decode the same files.

The fake helper (`internal/helper/helpertest`) can replay a fixture (`replay=PATH`).

Method names past the handshake are provisional until the M0 spikes; change a fixture and the Go types together.
