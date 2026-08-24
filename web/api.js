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
