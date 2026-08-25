// ── section: translator — DeepL-inspired layout: two large side-by-side
// panes, auto language detection, live translation ~800ms after typing
// pauses (Seungheon's debounce), copy buttons, character count. Business
// Version rides below the output pane. Pairs with translate.go.
const { useState: useStateTranslate, useRef: useRefTranslate } = React;

const TRANSLATE_MAX_CHARS = 500;

function TranslateView() {
  const [text, setText] = useStateTranslate('');
  const [result, setResult] = useStateTranslate(null);
  const [error, setError] = useStateTranslate('');
  const [loading, setLoading] = useStateTranslate(false);
  const [copied, setCopied] = useStateTranslate(''); // 'main' | 'biz' | ''
  const requestSeq = useRefTranslate(0);
  const debounceTimer = useRefTranslate(null);
  const lastTranslated = useRefTranslate('');
  const copiedTimer = useRefTranslate(null);

  const translate = async (input) => {
    const trimmed = (input !== undefined ? input : text).trim();
    if (!trimmed || trimmed === lastTranslated.current) return;
    lastTranslated.current = trimmed;
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

  const onTextChange = (e) => {
    const val = e.target.value.slice(0, TRANSLATE_MAX_CHARS);
    setText(val);
    clearTimeout(debounceTimer.current);
    if (val.trim() && val.trim() !== lastTranslated.current) {
      debounceTimer.current = setTimeout(() => translate(val), 800);
    }
  };

  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      clearTimeout(debounceTimer.current);
      translate();
    }
  };

  const copy = (which, value) => {
    if (!value || !navigator.clipboard) return;
    navigator.clipboard.writeText(value).then(() => {
      setCopied(which);
      clearTimeout(copiedTimer.current);
      copiedTimer.current = setTimeout(() => setCopied(''), 1200);
    });
  };

  const sourceLabel = result
    ? (result.detected_lang === 'ko' ? 'Korean (detected)' : 'English (detected)')
    : 'Detect language';
  const targetLabel = result
    ? (result.detected_lang === 'ko' ? 'English' : 'Korean')
    : 'Translation';

  return (
    <div className="deepl-wrap">
      <div className="deepl-panes">
        <div className="deepl-pane">
          <div className="deepl-pane-head">
            <span className="deepl-lang">{sourceLabel}</span>
          </div>
          <textarea
            className="deepl-input"
            value={text}
            onChange={onTextChange}
            onKeyDown={onKeyDown}
            placeholder="Type to translate."
            autoFocus
          />
          <div className="deepl-pane-foot">
            <span className="deepl-count">{text.length}/{TRANSLATE_MAX_CHARS}</span>
          </div>
        </div>

        <div className="deepl-divider" aria-hidden="true">⇄</div>

        <div className={`deepl-pane output ${loading ? 'loading' : ''}`}>
          <div className="deepl-pane-head">
            <span className="deepl-lang">{targetLabel}</span>
            {loading && <span className="deepl-status">Translating…</span>}
          </div>
          <div className="deepl-output">
            {result ? result.translation : <span className="deepl-placeholder">Translation appears here as you type</span>}
          </div>
          <div className="deepl-pane-foot">
            {result && (
              <button className="deepl-copy" onClick={() => copy('main', result.translation)}>
                {copied === 'main' ? '✓ Copied' : '⧉ Copy'}
              </button>
            )}
          </div>
        </div>
      </div>

      <div className={`card deepl-biz ${loading ? 'loading' : ''}`}>
        <div className="deepl-pane-head">
          <span className="deepl-lang gold">💼 Business Version</span>
          {result && (
            <button className="deepl-copy" onClick={() => copy('biz', result.business_version)}>
              {copied === 'biz' ? '✓ Copied' : '⧉ Copy'}
            </button>
          )}
        </div>
        <div className="deepl-output biz">
          {result ? result.business_version : <span className="deepl-placeholder">A polished, professional business-English rewrite of your text</span>}
        </div>
      </div>

      {error && <div className="state-msg">{error}</div>}
    </div>
  );
}
