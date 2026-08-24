// ── section: 퀴즈 (하루 10문제, 객관식 + 이어맞추기) — pairs with quiz.go.
// Independent of the other sections; only shares api() (api.js) and the
// score callback from app.jsx.
const { useState: useStateQuiz, useEffect: useEffectQuiz, useCallback: useCallbackQuiz } = React;

function QuizProgress({ answered, total, correct }) {
  const pct = total ? Math.round((answered / total) * 100) : 0;
  return (
    <div className="quiz-progress">
      <div className="quiz-progress-track">
        <div className="quiz-progress-fill" style={{ width: `${pct}%` }} />
      </div>
      <div className="quiz-progress-label">오늘 {answered}/{total} 문제 · 정답 {correct}개</div>
    </div>
  );
}

function MultipleChoiceQuestion({ q, answer, onChoose }) {
  return (
    <div className="quiz-options">
      {q.options.map((opt) => {
        let cls = 'quiz-option';
        if (answer) {
          if (opt.id === answer.correctId) cls += ' correct';
          else if (opt.id === answer.selected) cls += ' wrong';
        }
        return (
          <button key={opt.id} className={cls} onClick={() => onChoose(opt.id)} disabled={!!answer}>
            {opt.korean_text}
          </button>
        );
      })}
    </div>
  );
}

function WordOrderQuestion({ q, constructed, answer, onTap, onRemove, onSubmit }) {
  const byId = {};
  q.options.forEach((opt) => { byId[opt.id] = opt.text; });
  const remaining = q.options.filter((opt) => !constructed.includes(opt.id));
  const correctSentence = answer && answer.correctOrder ? answer.correctOrder.map((id) => byId[id]).join(' ') : '';

  return (
    <div className="word-order">
      <div className="word-order-build">
        {constructed.length === 0 && <span className="word-order-placeholder">아래 단어를 순서대로 눌러서 문장을 완성하세요</span>}
        {constructed.map((id, i) => (
          <button key={id} className="word-chip placed" onClick={() => !answer && onRemove(i)} disabled={!!answer}>
            {byId[id]}
          </button>
        ))}
      </div>
      <div className="word-order-pool">
        {remaining.map((opt) => (
          <button key={opt.id} className="word-chip" onClick={() => onTap(opt.id)} disabled={!!answer}>
            {opt.text}
          </button>
        ))}
      </div>
      {!answer && (
        <button className="duo-btn blue" disabled={constructed.length !== q.options.length} onClick={onSubmit}>
          확인
        </button>
      )}
      {answer && !answer.correct && (
        <div className="word-order-answer">정답: {correctSentence}</div>
      )}
    </div>
  );
}

function QuizView({ onScoreChange, showToast }) {
  const [state, setState] = useStateQuiz(null); // { questions, total, answered_count, correct_count, message }
  const [error, setError] = useStateQuiz('');
  const [answer, setAnswer] = useStateQuiz(null); // { correct, selected?, correctId?, correctOrder? }
  const [constructed, setConstructed] = useStateQuiz([]);
  // currentId is pinned explicitly (not re-derived from state.questions every
  // render) — grading a question immediately flips its `result` in state, so
  // deriving "the current question" via findIndex(!result) on every render
  // would jump to the NEXT question the instant it's graded, showing the
  // just-answered feedback against the wrong question's data. `undefined` =
  // not yet initialized, `null` = today's set is fully done.
  const [currentId, setCurrentId] = useStateQuiz(undefined);

  const load = useCallbackQuiz(() => {
    api('/api/quiz/today').then(setState).catch((e) => setError(e.message));
  }, []);

  useEffectQuiz(() => { load(); }, [load]);

  useEffectQuiz(() => {
    if (state && state.questions && currentId === undefined) {
      const first = state.questions.find((q) => !q.result);
      setCurrentId(first ? first.id : null);
    }
  }, [state, currentId]);

  if (error) return <div className="state-msg">{error}</div>;
  if (!state) return <div className="state-msg">오늘의 퀴즈 불러오는 중...</div>;
  if (state.message) return <div className="state-msg">{state.message}</div>;
  if (currentId === undefined) return <div className="state-msg">오늘의 퀴즈 불러오는 중...</div>;

  const done = currentId === null;
  const q = done ? null : state.questions.find((item) => item.id === currentId);

  const applyGraded = (selected, data) => {
    onScoreChange(data.score);
    if (data.score_delta > 0) showToast(`+${data.score_delta} 💎`);
    setAnswer({ correct: data.correct, selected, correctId: data.correct_id, correctOrder: data.correct_order });
    setState((prev) => ({
      ...prev,
      questions: prev.questions.map((item) =>
        item.id === q.id ? { ...item, result: data.correct ? 'correct' : 'incorrect' } : item
      ),
      answered_count: prev.answered_count + 1,
      correct_count: prev.correct_count + (data.correct ? 1 : 0),
    }));
  };

  const choose = async (optionId) => {
    if (answer) return;
    try {
      const data = await api('/api/quiz/answer', { method: 'POST', body: { question_id: q.id, selected_id: optionId } });
      applyGraded(optionId, data);
    } catch (e) { showToast(e.message); }
  };

  const submitWordOrder = async () => {
    try {
      const data = await api('/api/quiz/answer', { method: 'POST', body: { question_id: q.id, ordered_ids: constructed } });
      applyGraded(null, data);
    } catch (e) { showToast(e.message); }
  };

  const next = () => {
    setAnswer(null);
    setConstructed([]);
    const upcoming = state.questions.find((item) => !item.result);
    setCurrentId(upcoming ? upcoming.id : null);
  };

  if (done) {
    return (
      <div className="quiz-wrap">
        <div className="tagline">Business English · 오늘의 퀴즈</div>
        <QuizProgress answered={state.answered_count} total={state.total} correct={state.correct_count} />
        <div className="card quiz-complete">
          <div className="quiz-complete-emoji">🎉</div>
          <div className="quiz-complete-title">오늘 {state.total}문제 다 풀었어요!</div>
          <div className="quiz-complete-sub">정답 {state.correct_count}개 · 내일 새로운 {state.total}문제로 다시 만나요</div>
        </div>
      </div>
    );
  }

  return (
    <div className="quiz-wrap">
      <div className="tagline">Business English · 오늘의 퀴즈</div>
      <QuizProgress answered={state.answered_count} total={state.total} correct={state.correct_count} />

      <div className="quiz-type-badge">{q.question_type === 'multiple_choice' ? '객관식' : '이어맞추기'}</div>
      <div className="card quiz-question">{q.prompt}</div>

      {q.question_type === 'multiple_choice' ? (
        <MultipleChoiceQuestion q={q} answer={answer} onChoose={choose} />
      ) : (
        <WordOrderQuestion
          q={q}
          constructed={constructed}
          answer={answer}
          onTap={(id) => setConstructed((c) => [...c, id])}
          onRemove={(i) => setConstructed((c) => c.filter((_, idx) => idx !== i))}
          onSubmit={submitWordOrder}
        />
      )}

      {answer && (
        <div className="quiz-feedback">
          <div>{answer.correct ? '정답이에요! 🎉' : '아쉬워요, 다음에 맞혀봐요.'}</div>
          <button className="duo-btn" onClick={next}>다음 문제</button>
        </div>
      )}
    </div>
  );
}
