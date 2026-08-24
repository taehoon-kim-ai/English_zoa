// ── section: translator — 3rd main tab. Pairs with translate.go. Split-pane
// layout: type on the left, translation appears live on the right (debounced
// ~900ms after the last keystroke — each translation is a real AI call, so
// no request per keystroke). The bottom-right panel is the "Business
// Version": a professional business-English rewrite, not a literal
// translation.
const { useState: useStateTranslate, useEffect: useEffectTranslate, useRef: useRefTranslate } = React;

const TRANSLATE_DEBOUNCE_MS = 900;

function TranslateView() {
  const [text, setText] = useStateTranslate('');
  const [result, setResult] = useStateTranslate(null);
  const [error, setError] = useStateTranslate('');
  const [loading, setLoading] = useStateTranslate(false);
  // Serial number for in-flight requests — a slow older response must never
  // overwrite the result of a newer one typed after it.
  const requestSeq = useRefTranslate(0);

  useEffectTranslate(() => {
    const trimmed = text.trim();
    if (!trimmed) {
      setResult(null);
      setError('');
      setLoading(false);
      return;
    }
    setLoading(true);
    const seq = ++requestSeq.current;
    const timer = setTimeout(async () => {
      try {
        const data = await api('/api/translate', { method: 'POST', body: { text: trimmed } });
        if (seq !== requestSeq.current) return; // stale response — a newer keystroke superseded this request
        setResult(data);
        setError('');
      } catch (e) {
        if (seq !== requestSeq.current) return;
        setError(e.message);
        setResult(null);
      } finally {
        if (seq === requestSeq.current) setLoading(false);
      }
    }, TRANSLATE_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [text]);

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
            placeholder="Start typing... e.g. 이거 언제까지 끝낼 수 있어요?"
          />
        </div>

        <div className="translate-pane-col">
          <div className={`card translate-pane ${loading ? 'loading' : ''}`}>
            <div className="mini-lb-title">
              Translation
              {result && (result.detected_lang === 'ko' ? ' (Korean → English)' : ' (English → Korean)')}
              {loading && ' · translating...'}
            </div>
            <div className="translate-text">
              {result ? result.translation : <span className="translate-placeholder">Translation appears here as you type</span>}
            </div>
          </div>

          <div className={`card translate-pane business ${loading ? 'loading' : ''}`}>
            <div className="mini-lb-title">💼 Business Version{loading && ' · thinking...'}</div>
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
