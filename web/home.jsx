// ── section: 오늘의 문구 (플래시카드) — pairs with phrase.go ─────────────────
const { useState: useStateHome, useEffect: useEffectHome } = React;

function HomeView({ me, onScoreChange, showToast }) {
  const [phrase, setPhrase] = useStateHome(null);
  const [attempt, setAttempt] = useStateHome('');
  const [flipped, setFlipped] = useStateHome(false);
  const [error, setError] = useStateHome('');

  useEffectHome(() => {
    api('/api/phrase/today')
      .then((data) => {
        setPhrase(data.phrase);
        setAttempt(data.attempt || '');
      })
      .catch((e) => setError(e.message));
  }, []);

  const answer = async (result) => {
    if (!phrase) return;
    try {
      const data = await api('/api/phrase/attempt', { method: 'POST', body: { phrase_id: phrase.id, result } });
      setAttempt(result);
      onScoreChange(data.score);
      if (data.score_delta > 0) showToast(`+${data.score_delta} 💎`);
      else if (data.score_delta < 0) showToast(`${data.score_delta} 💎`);
    } catch (e) {
      showToast(e.message);
    }
  };

  if (error) return <div className="state-msg">{error}</div>;
  if (!phrase) return <div className="state-msg">오늘의 비즈니스 영어 문구를 불러오는 중...</div>;

  return (
    <div className="flashcard-wrap">
      <div className="tagline">Business English · Phrase of the Day</div>
      {me && me.streak > 0 && (
        <div className="streak-banner">🔥 {me.streak}일 연속 학습 중이에요!</div>
      )}
      <div className="flashcard-scene" onClick={() => setFlipped((f) => !f)}>
        <div className={`flashcard ${flipped ? 'flipped' : ''}`}>
          <div className="flashcard-face front">{phrase.english_text}</div>
          <div className="flashcard-face back">{phrase.korean_text}</div>
        </div>
      </div>
      <div className="flashcard-hint">카드를 클릭하면 뒤집혀요</div>
      <div className="answer-buttons">
        <button className={`duo-btn ${attempt === 'known' ? 'selected-know' : ''}`} onClick={() => answer('known')}>
          알아요 ✓
        </button>
        <button className={`duo-btn red ${attempt === 'unknown' ? 'selected-dont' : ''}`} onClick={() => answer('unknown')}>
          몰라요 ✕
        </button>
      </div>
    </div>
  );
}
