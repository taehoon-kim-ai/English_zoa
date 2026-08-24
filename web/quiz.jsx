// ── section: 퀴즈 (4지선다) — pairs with quiz.go. Independent of home.jsx;
// only shares api() (api.js) and the score callback from app.jsx.
const { useState: useStateQuiz, useEffect: useEffectQuiz, useCallback: useCallbackQuiz } = React;

function QuizView({ onScoreChange, showToast }) {
  const [quiz, setQuiz] = useStateQuiz(null);
  const [message, setMessage] = useStateQuiz('');
  const [selected, setSelected] = useStateQuiz(null);
  const [result, setResult] = useStateQuiz(null); // { correct }
  const [loading, setLoading] = useStateQuiz(true);

  const loadNext = useCallbackQuiz(() => {
    setSelected(null);
    setResult(null);
    setLoading(true);
    api('/api/quiz/next')
      .then((data) => {
        setQuiz(data.quiz);
        setMessage(data.message || '');
      })
      .catch((e) => setMessage(e.message))
      .finally(() => setLoading(false));
  }, []);

  useEffectQuiz(() => { loadNext(); }, [loadNext]);

  const choose = async (optionId) => {
    if (result || !quiz) return;
    setSelected(optionId);
    try {
      const data = await api('/api/quiz/answer', {
        method: 'POST',
        body: { phrase_id: quiz.phrase_id, selected_id: optionId },
      });
      setResult({ correct: data.correct });
      onScoreChange(data.score);
      if (data.score_delta > 0) showToast(`+${data.score_delta} 💎`);
    } catch (e) {
      showToast(e.message);
    }
  };

  if (loading) return <div className="state-msg">퀴즈 불러오는 중...</div>;
  if (!quiz) return <div className="state-msg">{message || '아직 퀴즈를 만들 문구가 부족해요. 며칠만 더 기다려주세요!'}</div>;

  return (
    <div className="quiz-wrap">
      <div className="tagline">뜻 맞히기 퀴즈</div>
      {!quiz.scored && <div className="quiz-practice-badge">연습 모드 — 이미 풀었던 문제예요</div>}
      <div className="card quiz-question">{quiz.english_text}</div>
      <div className="quiz-options">
        {quiz.options.map((opt) => {
          let cls = 'quiz-option';
          if (result) {
            if (opt.id === quiz.phrase_id) cls += ' correct';
            else if (opt.id === selected) cls += ' wrong';
          } else if (opt.id === selected) {
            cls += ' selected';
          }
          return (
            <button key={opt.id} className={cls} onClick={() => choose(opt.id)} disabled={!!result}>
              {opt.korean_text}
            </button>
          );
        })}
      </div>
      {result && (
        <div className="quiz-feedback">
          <div>{result.correct ? '정답이에요! 🎉' : '아쉬워요, 다음에 맞혀봐요.'}</div>
          <button className="duo-btn blue" onClick={loadNext}>다음 문제</button>
        </div>
      )}
    </div>
  );
}
