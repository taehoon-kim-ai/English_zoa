// ── section: translator — 3rd main tab. Pairs with translate.go. Split-pane
// layout: type on the left, translation + Business Version on the right.
// Translation fires only on an explicit action (Enter or the button), never
// automatically while typing — every uncached translate is a real AI call,
// so no request per keystroke/pause. Shift+Enter inserts a newline. Repeat
// inputs are served from a server-side cache (shown with a ⚡ badge).
const { useState: useStateTranslate, useRef: useRefTranslate } = React;

function TranslateView() {
  const [text, setText] = useStateTranslate('');
  const [result, setResult] = useStateTranslate(null);
  const [error, setError] = useStateTranslate('');
  const [loading, setLoading] = useStateTranslate(false);
  // Serial number for in-flight requests — a slow older response must never
  // overwrite the result of a newer request fired after it.
  const requestSeq = useRefTranslate(0);

  const translate = async () => {
    const trimmed = text.trim();
    if (!trimmed || loading) return;
    setLoading(true);
    setError('');
    const seq = ++requestSeq.current;
    try {
      const data = await api('/api/translate', { method: 'POST', body: { text: trimmed } });
      if (seq !== requestSeq.current) return;
      setResult(data);
    } catch (e) {
      if (seq !== requestSeq.current) return;
      setError(e.message);
      setResult(null);
    } finally {
      if (seq === requestSeq.current) setLoading(false);
    }
  };

  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      translate();
    }
  };

  return (
    <div className="quiz-wrap">
      <div className="tagline">🌐 Translator</div>

      <div className="translate-grid">
        <div className="card translate-pane">
          <div className="mini-lb-title">Korean or English</div>
          <textarea
            className="translate-input tall"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="Type and press Enter... e.g. 이거 언제까지 끝낼 수 있어요? (Shift+Enter for a new line)"
          />
          <button className="duo-btn blue" onClick={translate} disabled={loading || !text.trim()}>
            {loading ? 'Translating...' : 'Translate ⏎'}
          </button>
        </div>

        <div className="translate-pane-col">
          <div className={`card translate-pane ${loading ? 'loading' : ''}`}>
            <div className="mini-lb-title">
              Translation
              {result && (result.detected_lang === 'ko' ? ' (Korean → English)' : ' (English → Korean)')}
              {result && result.cached && ' ⚡'}
            </div>
            <div className="translate-text">
              {result ? result.translation : <span className="translate-placeholder">Press Enter to translate</span>}
            </div>
          </div>

          <div className={`card translate-pane business ${loading ? 'loading' : ''}`}>
            <div className="mini-lb-title">💼 Business Version</div>
            <div className="translate-text">
              {result ? result.business_version : <span className="translate-placeholder">A professional business-English rewrite</span>}
            </div>
          </div>
        </div>
      </div>

      {error && <div className="state-msg">{error}</div>}
    </div>
  );
}
