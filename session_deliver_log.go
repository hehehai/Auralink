package main

import (
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
)

// logDelivery records a delivery the user started from the app itself
// (the card's Insert, paste the last result): one INFO line in the
// session's source, as a session's own delivery (deliver.Result.Describe).
func (a *app) logDelivery(what string, res deliver.Result, target proto.App) {
	a.logs.Append(core.LogEntry{Time: time.Now(), Level: core.Severity("info"), Source: sessionSource,
		Message: "delivery (" + what + "): " + res.Describe(target)})
}
