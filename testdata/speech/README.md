# Speech sample of the end-to-end tests

`sample-zh-en.wav`: about 5 seconds of speech mixing Chinese and English
(for example "今天我们用 Auralink 测试一下 speech recognition。"), as a
16 kHz, mono, 16-bit PCM WAV file. The `TestE2E` tests of
`internal/asr/{openai,doubao,aliyun}` recognise it with the real services
when `AURALINK_E2E=1` and their keys are set; without the file they skip.

Only commit a recording whose licence allows it (your own voice, or a
TTS output whose terms allow redistribution), and say here where it came
from.
