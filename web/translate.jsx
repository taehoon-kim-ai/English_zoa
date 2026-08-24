// ── section: translator — 3rd main tab. Pairs with translate.go. Detects
// Korean vs English and translates to the other language, plus a separate
// "Business Version" that's a professional business-English rewrite (not
// just a literal translation — an upgrade even when the input is already
// English).
const { useState: useStateTranslate } = React;

function TranslateView() {
  const [text, setText] = useStateTranslate('');
  const [result, setResult] = useStateTranslate(null);
  const [error, setError] = useStateTranslate('');
  const [loading, setLoading] = useStateTranslate(false);

  const translate = async () => {
    const trimmed = text.trim();
    if (!trimmed) return;
    setLoading(true);
    setError('');
    try {
      const data = await api('/api/translate', { method: 'POST', body: { text: trimmed } });
      setResult(data);
    } catch (e) {
      setError(e.message);
      setResult(null);
    } finally {
      setLoading(false);
    }
  };

  const onKeyDown = (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) translate();
  };

  return (
    <div className="quiz-wrap">
      <div className="tagline">🌐 Translator</div>

      <div className="card" style={{ width: '100%' }}>
        <div className="profile-field">
          <label>Type in Korean or English</label>
          <textarea
            className="translate-input"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="e.g. 이거 언제까지 끝낼 수 있어요? / Can you send this over by Friday?"
            rows={3}
          />
        </div>
        <button className="duo-btn blue" onClick={translate} disabled={loading || !text.trim()}>
          {loading ? 'Translating...' : 'Translate'}
        </button>
      </div>

      {error && <div className="state-msg">{error}</div>}

      {result && (
        <>
          <div className="card translate-result">
            <div className="mini-lb-title">
              Translation {result.detected_lang === 'ko' ? '(Korean → English)' : '(English → Korean)'}
            </div>
            <div className="translate-text">{result.translation}</div>
          </div>
          <div className="card translate-result business">
            <div className="mini-lb-title">💼 Business Version</div>
            <div className="translate-text">{result.business_version}</div>
          </div>
        </>
      )}
    </div>
  );
}
