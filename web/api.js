// Shared fetch helper — plain script (no JSX), loaded first so every
// section file (home.jsx, quiz.jsx, profile.jsx, leaderboard.jsx, app.jsx)
// can call api(...) directly. Function declarations here become properties
// of the shared global scope across <script> tags, same as React components
// declared with `function X() {}` in the other files.
async function api(path, opts) {
  const res = await fetch(path, {
    method: (opts && opts.method) || 'GET',
    headers: opts && opts.body ? { 'Content-Type': 'application/json' } : undefined,
    body: opts && opts.body ? JSON.stringify(opts.body) : undefined,
  });
  if (!res.ok) {
    const detail = await res.json().catch(() => ({}));
    throw new Error(detail.error || `HTTP ${res.status}`);
  }
  return res.json();
}

// Speak English text with the browser's built-in TTS — free, no API calls.
// The browser DEFAULT voice is often a terrible robotic one, so we pick the
// best installed English voice by a quality-ordered preference list (natural
// female voices first: macOS Samantha/Ava, Chrome's Google US English,
// Windows Aria/Jenny/Zira, ...). Voices load asynchronously, so the list is
// resolved lazily and re-resolved on voiceschanged.
const TTS_PREFERRED_VOICES = [
  'Samantha',            // macOS — natural female
  'Ava (Premium)', 'Ava',
  'Google US English',   // Chrome — natural female
  'Microsoft Aria Online (Natural) - English (United States)',
  'Microsoft Jenny Online (Natural) - English (United States)',
  'Microsoft Zira - English (United States)',
  'Allison', 'Susan', 'Karen', 'Moira', 'Serena',
];

let ttsVoice = null;

function pickBestEnglishVoice() {
  const voices = window.speechSynthesis.getVoices();
  if (!voices.length) return null;
  for (const name of TTS_PREFERRED_VOICES) {
    const v = voices.find((v) => v.name === name);
    if (v) return v;
  }
  // Any female-sounding en voice, then any en-US, then any en.
  return (
    voices.find((v) => v.lang.startsWith('en') && /female|woman/i.test(v.name)) ||
    voices.find((v) => v.lang === 'en-US') ||
    voices.find((v) => v.lang.startsWith('en')) ||
    null
  );
}

if ('speechSynthesis' in window) {
  ttsVoice = pickBestEnglishVoice();
  window.speechSynthesis.onvoiceschanged = () => { ttsVoice = pickBestEnglishVoice(); };
}

function speakEnglish(text) {
  if (!('speechSynthesis' in window) || !text) return;
  window.speechSynthesis.cancel();
  const u = new SpeechSynthesisUtterance(text);
  u.lang = 'en-US';
  u.rate = 0.95;
  u.pitch = 1.02;
  if (!ttsVoice) ttsVoice = pickBestEnglishVoice(); // voices may have loaded since startup
  if (ttsVoice) u.voice = ttsVoice;
  window.speechSynthesis.speak(u);
}
