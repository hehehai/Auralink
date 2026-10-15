# Helper protocol fixtures

Golden transcripts of the helper protocol (`docs/helper-protocol.md`), one JSON object per line:

```json
{"to":"helper","msg":{…}}   // app → helper (a request)
{"to":"app","msg":{…}}      // helper → app (a response or a notification)
```

The format is AudioSmith's engine fixtures', with the helper in the engine's place. Both sides decode every line:

- `go test ./internal/helper/proto` decodes each line strictly with the Go types (unknown fields fail), checks that it encodes back to the same JSON, and checks the sequencing rules: one response per request, a job's notifications before its response and after its `capture.started`, frames without gaps in `seq` and `offset` and counted by the result, `job.cancel` and `capture.stop` answered after the job they end, valid hotkey bindings and events. `TestFixturesCoverMethods` fails when a method has no fixture; `TestHelloListsEveryRequest` when the handshake does not list every request.
- `cd helper && swift test` (FixtureTests) decodes each line with the Swift types and checks that it encodes back to the same JSON: a field one side lacks, or with another optionality, fails.

The fake helper (`internal/helper/helpertest`) can replay a fixture (`replay=PATH`).

Change a fixture together with the Go types, the Swift types and the spec.
