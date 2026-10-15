# Changelog

All notable changes to Auralink. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [Semantic Versioning](https://semver.org/).

## [0.1.0] - Unreleased

The first version: voice input for macOS on remote models.

### Added

- **Transcribe**: a shortcut (fn by default; tap, double tap or long press, several per feature) records, a speech service recognises, a language model cleans up (fillers, false starts, punctuation, numbers; an optional structure layer and your own rules), and the text is pasted where the cursor is. Without a language model the text goes in as recognised; when it fails or is slow, too.
- **Translate**: the selection (read through Accessibility, or ⌘C where that is safe) or what you say, into the language you choose; a strict retry when the answer looks untranslated; an answer card to read, copy, insert or translate again into another language.
- **Rewrite**: say how to change the selection, or what to write with nothing selected; answers stream into the card when they are long or the target cannot take text; follow-ups refine the answer.
- **App enhancement**: groups of rules by app and by website (address rules read through Accessibility, no browser automation), a scene prompt layered on top of the clean-up, the key to press after inserting, the features each group applies to, a live match preview, and the group's label in the overlay and the history.
- **Dictionary**: terms with aliases, sent as hints to the speech services and as a glossary to the language model; CSV and text import and export.
- **Model services**: Doubao and Alibaba Cloud Model Studio streaming recognition and any OpenAI-compatible transcription API; DeepSeek, Qwen, Volcengine Ark, Kimi, GLM, MiniMax, StepFun, MiMo, SiliconFlow, OpenAI, Anthropic, Gemini, OpenRouter and other OpenAI-compatible language models; keys in the Keychain, connection tests, a service per feature.
- **Home**: what is still missing (a service, a permission, the shortcut) with the way to fix it, a try of the whole chain on the page, today's results and characters, the last five results.
- **Setup guide** on the first start (and from Settings → General): privacy, permissions, services, then a real try of Transcribe, Translate and Rewrite, and App enhancement and the dictionary.
- **History**: by feature, searchable, with the texts, where the result went, the services, the rule that matched and each stage's time; recordings kept on request.
- The recording overlay (non-activating, so the app you type into keeps its focus) and the answer card; the menu bar icon; Settings: General, Permissions, Audio, Overlay, Shortcuts, Appearance, Network (proxy), Privacy, Storage, Diagnostics and about; English and Simplified Chinese.

### Known limitations

- Builds are ad hoc signed and not notarised: macOS asks you to confirm the first start, and forgets the Accessibility grant after each update (README, Install).
- Apple silicon and macOS 15 or later only.
- The app icon is a placeholder.
