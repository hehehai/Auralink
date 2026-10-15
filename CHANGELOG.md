# Changelog

All notable changes to Auralink. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [Semantic Versioning](https://semver.org/).

## [0.1.0] - Unreleased

The first version: voice input for macOS on remote models.

### Added

- **Transcribe**: a shortcut (fn by default; tap, double tap or long press, several per feature) records, a speech service recognises, a language model cleans up (fillers, false starts, punctuation, numbers; an optional structure layer and your own rules), and the text is pasted where the cursor is. Without a language model the text goes in as recognised; when it fails or is slow, too.
- **Translate**: the selection (read through Accessibility, or ⌘C where that is safe) or what you say, into the language you choose; a strict retry when the answer looks untranslated; an answer card to read, copy, insert or translate again into another language.
- **Rewrite**: say how to change the selection, or what to write with nothing selected; every answer streams into the answer card (as in Voxt), where Insert puts it in place (over the selection) and Continue records a follow-up, answered in plain text in the card's conversation.
- **App enhancement**: groups of rules by app and by website (address rules read through Accessibility, no browser automation), a scene prompt layered on top of the clean-up, the key to press after inserting, the features each group applies to, a live match preview, and the group's label in the overlay and the history.
- **Dictionary**: terms with aliases, sent as hints to the speech services and as a glossary to the language model; CSV and text import and export.
- **Model services**: Doubao and Alibaba Cloud Model Studio streaming recognition and any OpenAI-compatible transcription API; DeepSeek, Qwen, Volcengine Ark, Kimi, GLM, MiniMax, StepFun, MiMo, SiliconFlow, OpenAI, Anthropic, Gemini, OpenRouter and other OpenAI-compatible language models; keys in the Keychain, connection tests, a service per feature.
- **Home**: a slogan with your Transcribe shortcut in a key cap; what is still missing (a service, a permission, the shortcut), each line the way to fix it, and nothing once all is ready; a try of the whole chain on the page until the first result; today's results and characters, the last five results.
- **Setup guide** on the first start (and from Settings → General): privacy, permissions, services, then a real try of Transcribe, Translate and Rewrite, and App enhancement and the dictionary.
- **History**: by feature, searchable, with the texts, where the result went, the services, the rule that matched and each stage's time; recordings kept on request.
- The recording overlay (non-activating, so the app you type into keeps its focus), after Voxt's: the feature's icon and a waveform on a translucent, blurred pill with no buttons and Voxt's proportions (the shortcut stops, Escape cancels; the pointer goes through it), one line of live text under it, the pill as wide as the text up to 300 and the text then scrolled to its newest words, a loader while it works, errors in place of the waveform, nothing after an insert; and the answer card, with start and stop sounds. Every session starts as the pill, and it grows about its edge of the screen without moving.
- The answer card, after Voxt's: 480 wide (Voxt's is 560) and as high as its text (up to 220 of it, then it scrolls), its actions in the header (the target language as a capsule opening our own menu in a window of its own, Continue, Insert, Copy, Close), the header as far from the top as from the rule, Voxt's own icons in the pill and the card, why the result is in the card in Insert's tip instead of a line of orange, Copy marking itself for two seconds, Insert hiding the card while it pastes, another language translated in place (the old text with a spinner until the new one comes), and Rewrite follow-ups recorded in the card as a conversation (a small waveform, bubbles); Escape closes it, or cancels a follow-up only.
- Silence left out before recognition (a light energy detector, no model): a file service gets the recording without the silence around the speech and with long pauses shortened, a streaming service nothing before the speech starts, and a recording without speech is not sent at all (Settings → Audio → Skip silence).
- A small window (800×520 by default, 680×440 at the least) with no status bar: notifications stack as cards at the foot of the sidebar (a dot on Settings when it shows only icons), and a missing service or a stopped helper becomes a card with the action that fixes it.
- The menu bar icon: start or stop transcribing, paste the last result, the last three results to copy (one short line each, the whole in the tooltip); it changes while recording. Closing the window keeps Auralink there, and it can open at login.
- Settings: General (languages, the menu bar, login, the setup guide), Permissions, Audio (the microphones in your order of preference, skipping silence, sounds), Overlay style (place, opacity, corners, the recording time, with a live preview over a desktop picture), Shortcuts (every feature's keys and their conflicts), Appearance (the sidebar shrinks to icons in a narrow window), Network (proxy), Privacy, Storage, About (versions, the self-check, reporting a problem); English and Simplified Chinese.

### Known limitations

- Builds are ad hoc signed and not notarised: macOS asks you to confirm the first start, and forgets the Accessibility grant after each update (README, Install).
- Apple silicon and macOS 15 or later only.
- The app icon is a placeholder.
