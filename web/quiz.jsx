// ── section: quiz — Vocab Quiz / Phrase Quiz, each a user-picked test length.
// Pairs with quiz.go. Independent of the other sections; only shares api()
// (api.js) and the correct-count callback from app.jsx.
const { useState: useStateQuiz, useEffect: useEffectQuiz, useCallback: useCallbackQuiz } = React;

const TRACKS = [
  { key: 'vocab', title: 'Vocab Quiz', icon: '🔤', desc: 'Business terms & collocations', counts: [10, 20, 30] },
  { key: 'phrase', title: 'Phrase Quiz', icon: '💬', desc: 'Full workplace sentences', counts: [5, 10, 15] },
];

function TrackSelect({ onPick }) {
  return (
    <div className="quiz-wrap">
      <div className="tagline">Business English · Choose a Quiz</div>
      <div className="track-grid">
        {TRACKS.map((t) => (
          <button key={t.key} className="track-card" onClick={() => onPick(t)}>
            <div className="track-card-icon">{t.icon}</div>
            <div className="track-card-title">{t.title}</div>
            <div className="track-card-desc">{t.desc}</div>
          </button>
        ))}
      </div>
    </div>
  );
}

function CountSelect({ track, onPick, onBack }) {
  return (
    <div className="quiz-wrap">
      <div className="tagline">{track.icon} {track.title}</div>
      <div className="count-grid">
        {track.counts.map((c) => (
          <button key={c} className="duo-btn blue" onClick={() => onPick(c)}>{c} Questions</button>
        ))}
      </div>
      <button className="duo-btn outline" onClick={onBack}>‹ Back</button>
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
        {constructed.length === 0 && <span className="word-order-placeholder">Tap the words below in order to build the sentence</span>}
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
          Check
        </button>
      )}
      {answer && !answer.correct && (
        <div className="word-order-answer">Answer: {correctSentence}</div>
      )}
    </div>
  );
}

function QuizSession({ track, count, onCorrectCountChange, showToast, onRestart }) {
  const [state, setState] = useStateQuiz(null);
  const [error, setError] = useStateQuiz('');
  const [answer, setAnswer] = useStateQuiz(null);
  const [constructed, setConstructed] = useStateQuiz([]);
  const [currentId, setCurrentId] = useStateQuiz(undefined);

  useEffectQuiz(() => {
    api('/api/quiz/start', { method: 'POST', body: { track: track.key, count } })
      .then(setState)
      .catch((e) => setError(e.message));
  }, [track, count]);

  useEffectQuiz(() => {
    if (state && state.questions && currentId === undefined) {
      const first = state.questions.find((q) => !q.result);
      setCurrentId(first ? first.id : null);
    }
  }, [state, currentId]);

  if (error) return <div className="state-msg">{error}</div>;
  if (!state) return <div className="state-msg">Preparing your quiz...</div>;
  if (state.message) return <div className="state-msg">{state.message}</div>;
  if (currentId === undefined) return <div className="state-msg">Preparing your quiz...</div>;

  const done = currentId === null;
  const q = done ? null : state.questions.find((item) => item.id === currentId);

  const applyGraded = (selected, data) => {
    if (data.newly_correct) {
      onCorrectCountChange(data.correct_count);
      showToast('+1 🎯');
    }
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
        <div className="tagline">{track.icon} {track.title}</div>
        <QuizProgress answered={state.answered_count} total={state.total} correct={state.correct_count} />
        <div className="card quiz-complete">
          <div className="quiz-complete-emoji">🎉</div>
          <div className="quiz-complete-title">You finished all {state.total} questions!</div>
          <div className="quiz-complete-sub">{state.correct_count} correct</div>
          <button className="duo-btn" onClick={onRestart}>Take Another Quiz</button>
        </div>
      </div>
    );
  }

  return (
    <div className="quiz-wrap">
      <div className="tagline">{track.icon} {track.title}</div>
      <QuizProgress answered={state.answered_count} total={state.total} correct={state.correct_count} />

      <div className="quiz-badges">
        <span className="quiz-badge category">{q.category === 'vocabulary' ? 'Vocabulary' : 'Expression'}</span>
        <span className="quiz-badge type">{q.question_type === 'multiple_choice' ? 'Multiple Choice' : 'Word Order'}</span>
      </div>
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
          <div>{answer.correct ? 'Correct! 🎉' : "Not quite — you'll get the next one."}</div>
          <button className="duo-btn" onClick={next}>Next question</button>
        </div>
      )}
    </div>
  );
}

function QuizProgress({ answered, total, correct }) {
  const pct = total ? Math.round((answered / total) * 100) : 0;
  return (
    <div className="quiz-progress">
      <div className="quiz-progress-track">
        <div className="quiz-progress-fill" style={{ width: `${pct}%` }} />
      </div>
      <div className="quiz-progress-label">{answered}/{total} answered · {correct} correct</div>
    </div>
  );
}

// Sidebar shown on every quiz stage: login streak up top (same stat as the
// topbar/profile — not quiz-specific) + a per-day breakdown of how many
// questions were attempted/correct that day (quiz-specific, from quiz.go's
// getQuizHistory — distinct from the login streak).
function QuizHistoryPanel({ streak }) {
  const [days, setDays] = useStateQuiz(null);

  useEffectQuiz(() => {
    api('/api/quiz/history').then((data) => setDays(data.days || [])).catch(() => setDays([]));
  }, []);

  const flameCount = Math.max(Math.min(streak, 5), 1);

  return (
    <div className="card">
      <div className="streak-hero" style={{ marginBottom: 14, paddingBottom: 14 }}>
        <div className="streak-hero-flames">
          {Array.from({ length: flameCount }).map((_, i) => (
            <span key={i} className={`streak-hero-flame ${streak === 0 ? 'dim' : ''}`} style={{ animationDelay: `${i * 0.15}s` }}>🔥</span>
          ))}
        </div>
        <div className="streak-hero-count">{streak}</div>
        <div className="streak-hero-label">day streak</div>
      </div>
      <div className="mini-lb-title">📅 Quiz History</div>
      {days === null && <div className="state-msg">Loading...</div>}
      {days && days.length === 0 && <div className="mini-lb-empty">No quizzes taken yet</div>}
      {days && days.map((d) => (
        <div key={d.date} className="history-row">
          <span className="history-date">{d.date.slice(5)}</span>
          <span className="history-stats">{d.correct}/{d.attempted} correct</span>
        </div>
      ))}
    </div>
  );
}

function QuizView({ me, onCorrectCountChange, showToast }) {
  const [stage, setStage] = useStateQuiz('track'); // 'track' | 'count' | 'quiz'
  const [track, setTrack] = useStateQuiz(null);
  const [count, setCount] = useStateQuiz(null);
  const [runKey, setRunKey] = useStateQuiz(0); // bump to force a fresh QuizSession mount

  const pickTrack = (t) => { setTrack(t); setStage('count'); };
  const pickCount = (c) => { setCount(c); setStage('quiz'); setRunKey((k) => k + 1); };
  const restart = () => { setStage('track'); setTrack(null); setCount(null); };

  let content;
  if (stage === 'track') content = <TrackSelect onPick={pickTrack} />;
  else if (stage === 'count') content = <CountSelect track={track} onPick={pickCount} onBack={() => setStage('track')} />;
  else content = (
    <QuizSession
      key={runKey}
      track={track}
      count={count}
      onCorrectCountChange={onCorrectCountChange}
      showToast={showToast}
      onRestart={restart}
    />
  );

  return (
    <div className="quiz-page-grid">
      <div className="quiz-page-sidebar">
        <QuizHistoryPanel streak={me ? me.streak : 0} />
      </div>
      <div className="quiz-page-main">{content}</div>
    </div>
  );
}
