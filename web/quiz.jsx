// ── section: quiz — Vocab Quiz / Phrase Quiz, each a user-picked test length.
// Pairs with quiz.go. Independent of the other sections; only shares api()
// (api.js) and the correct-count callback from app.jsx.
const { useState: useStateQuiz, useEffect: useEffectQuiz, useCallback: useCallbackQuiz } = React;

// Pure-CSS confetti burst for the completion screen — ~40 pieces with
// randomized position/color/delay, generated once per mount.
const CONFETTI_COLORS = ['#58cc02', '#1cb0f6', '#ffc800', '#ff4b4b', '#ce82ff', '#ff9600'];

function ConfettiBurst() {
  const pieces = React.useMemo(() =>
    Array.from({ length: 40 }, (_, i) => ({
      left: Math.random() * 100,
      delay: Math.random() * 0.8,
      duration: 1.8 + Math.random() * 1.4,
      color: CONFETTI_COLORS[i % CONFETTI_COLORS.length],
      rotate: Math.random() * 360,
      size: 6 + Math.random() * 6,
    })), []);
  return (
    <div className="confetti-layer" aria-hidden="true">
      {pieces.map((p, i) => (
        <span
          key={i}
          className="confetti-piece"
          style={{
            left: `${p.left}%`,
            background: p.color,
            width: p.size,
            height: p.size * 0.6,
            animationDelay: `${p.delay}s`,
            animationDuration: `${p.duration}s`,
            transform: `rotate(${p.rotate}deg)`,
          }}
        />
      ))}
    </div>
  );
}

const TRACKS = [
  { key: 'vocab', title: 'Vocab Quiz', icon: '🔤', desc: 'Business terms & collocations', counts: [10, 20, 30] },
  { key: 'phrase', title: 'Phrase Quiz', icon: '💬', desc: 'Full workplace sentences', counts: [5, 10, 15] },
];

const REVIEW_TRACK = { key: 'review', title: 'Mistake Review', icon: '🔁', desc: 'Retry what you got wrong', counts: [], source: 'core' };
const BATTLE_TRACK = { key: 'battle', title: 'Word Battle', icon: '⚔️', desc: 'Live 1v1 — first to type it wins', counts: [] };

function TrackSelect({ onPick, onBattle }) {
  const section = (label, source) => (
    <div className="track-section">
      <div className="track-section-label">{label}</div>
      <div className="track-grid">
        {TRACKS.map((t) => (
          <button key={source + t.key} className="track-card" onClick={() => onPick({ ...t, source })}>
            <div className="track-card-icon">{t.icon}</div>
            <div className="track-card-title">{t.title}</div>
            <div className="track-card-desc">{t.desc}</div>
          </button>
        ))}
      </div>
    </div>
  );
  return (
    <div className="quiz-wrap">
      <div className="tagline">Business English · Choose a Quiz</div>
      {section('Section 1 · Word Bank (Slack + curated + AI)', 'core')}
      {section('Section 2 · From TED Talks & Daily News', 'media')}
      <div className="track-section">
        <div className="track-section-label">Extras</div>
        <div className="track-grid">
          <button className="track-card review" onClick={() => onPick(REVIEW_TRACK)}>
            <div className="track-card-icon">{REVIEW_TRACK.icon}</div>
            <div className="track-card-title">{REVIEW_TRACK.title}</div>
            <div className="track-card-desc">{REVIEW_TRACK.desc}</div>
          </button>
          <button className="track-card battle" onClick={onBattle}>
            <div className="track-card-icon">{BATTLE_TRACK.icon}</div>
            <div className="track-card-title">{BATTLE_TRACK.title}</div>
            <div className="track-card-desc">{BATTLE_TRACK.desc}</div>
          </button>
        </div>
      </div>
    </div>
  );
}

const DIFFICULTIES = [
  { key: 'easy', label: 'Easy', desc: '3 options · multiple choice only' },
  { key: 'medium', label: 'Medium', desc: '4 options · mixed types' },
  { key: 'hard', label: 'Hard', desc: '6 options · word-order phrases' },
];

function CountSelect({ track, onPick, onBack }) {
  const [difficulty, setDifficulty] = useStateQuiz('medium');
  return (
    <div className="quiz-wrap">
      <div className="tagline">{track.icon} {track.title}</div>
      <div className="difficulty-row">
        {DIFFICULTIES.map((d) => (
          <button
            key={d.key}
            className={`difficulty-chip ${difficulty === d.key ? 'active' : ''}`}
            onClick={() => setDifficulty(d.key)}
            title={d.desc}
          >
            {d.label}
          </button>
        ))}
      </div>
      <div className="count-grid">
        {track.counts.map((c) => (
          <button key={c} className="duo-btn blue" onClick={() => onPick(c, difficulty)}>{c} Questions</button>
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

function QuizSession({ track, count, difficulty, onCorrectCountChange, showToast, onRestart, onReview }) {
  const [state, setState] = useStateQuiz(null);
  const [error, setError] = useStateQuiz('');
  const [answer, setAnswer] = useStateQuiz(null);
  const [constructed, setConstructed] = useStateQuiz([]);
  const [currentId, setCurrentId] = useStateQuiz(undefined);

  useEffectQuiz(() => {
    api('/api/quiz/start', { method: 'POST', body: { track: track.key, count, source: track.source || 'core', difficulty: difficulty || 'medium' } })
      .then(setState)
      .catch((e) => setError(e.message));
  }, [track, count, difficulty]);

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
    const perfect = state.correct_count === state.total;
    return (
      <div className="quiz-wrap">
        <ConfettiBurst />
        <div className="tagline">{track.icon} {track.title}</div>
        <QuizProgress answered={state.answered_count} total={state.total} correct={state.correct_count} />
        <div className="card quiz-complete">
          <div className="quiz-complete-emoji">{perfect ? '🏆' : '🎉'}</div>
          <div className="quiz-complete-title">
            {perfect ? 'Perfect score!' : `You finished all ${state.total} questions!`}
          </div>
          <div className="quiz-complete-sub">{state.correct_count}/{state.total} correct</div>
          <div className="quiz-complete-actions">
            <button
              className="duo-btn blue"
              onClick={() => onReview({
                session_id: state.session_id,
                track: track.key,
                started_at: 'just now',
                total: state.total,
                correct: state.correct_count,
              })}
            >
              Review Answers
            </button>
            <button className="duo-btn" onClick={onRestart}>Take Another Quiz</button>
          </div>
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
      <div className="card quiz-question">
        {q.prompt}
        {q.question_type === 'multiple_choice' && (
          <button className="tts-btn" onClick={() => speakEnglish(q.prompt)} title="Listen">🔊</button>
        )}
      </div>

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
          <span className="history-stats">
            {d.vocab_attempted > 0 && <span className="history-track">🔤 {d.vocab_correct}/{d.vocab_attempted}</span>}
            {d.phrase_attempted > 0 && <span className="history-track">💬 {d.phrase_correct}/{d.phrase_attempted}</span>}
          </span>
        </div>
      ))}
    </div>
  );
}

// Past sessions list (quiz.go /api/quiz/sessions) — click one to review it.
function PastQuizzesPanel({ onReview, refreshKey }) {
  const [sessions, setSessions] = useStateQuiz(null);

  useEffectQuiz(() => {
    api('/api/quiz/sessions').then((data) => setSessions(data.sessions || [])).catch(() => setSessions([]));
  }, [refreshKey]);

  if (!sessions || sessions.length === 0) return null;

  return (
    <div className="card">
      <div className="mini-lb-title">📝 Past Quizzes</div>
      {sessions.map((s) => (
        <button key={s.session_id} className="session-row" onClick={() => onReview(s)}>
          <span className="session-track">{s.track === 'vocab' ? '🔤' : s.track === 'review' ? '🔁' : '💬'}</span>
          <span className="session-date">{s.started_at.slice(5, 16)}</span>
          <span className="session-score">{s.correct}/{s.total}</span>
          <span className="session-arrow">›</span>
        </button>
      ))}
    </div>
  );
}

// Read-only review of one past session: your answer vs the correct answer
// per question. Correct answers only show for questions that were graded.
function QuizReview({ session, onBack }) {
  const [items, setItems] = useStateQuiz(null);

  useEffectQuiz(() => {
    api(`/api/quiz/session/${encodeURIComponent(session.session_id)}/review`)
      .then((data) => setItems(data.items || []))
      .catch(() => setItems([]));
  }, [session]);

  return (
    <div className="quiz-wrap">
      <div className="tagline">
        {session.track === 'vocab' ? '🔤 Vocab Quiz' : session.track === 'review' ? '🔁 Mistake Review' : '💬 Phrase Quiz'} · {session.started_at} · {session.correct}/{session.total}
      </div>
      {items === null && <div className="state-msg">Loading review...</div>}
      {items && items.map((item) => (
        <div key={item.seq} className={`card review-item ${item.result}`}>
          <div className="review-head">
            <span className="review-result">{item.result === 'correct' ? '✅' : item.result === 'incorrect' ? '❌' : '⏳'}</span>
            <span className="review-prompt">{item.prompt}</span>
            {/[A-Za-z]/.test(item.prompt) && (
              <button className="tts-btn" onClick={() => speakEnglish(item.prompt)} title="Listen">🔊</button>
            )}
          </div>
          {item.result !== '' && (
            <div className="review-answers">
              <div className="review-line correct-line">
                <span className="review-label">Answer</span> {item.correct_text}
                {/[A-Za-z]/.test(item.correct_text) && (
                  <button className="tts-btn" onClick={() => speakEnglish(item.correct_text)} title="Listen">🔊</button>
                )}
              </div>
              {item.your_text && item.your_text !== item.correct_text && (
                <div className="review-line your-line">
                  <span className="review-label">You said</span> {item.your_text}
                </div>
              )}
            </div>
          )}
          {item.result === '' && <div className="review-answers"><div className="review-line">Not answered</div></div>}
        </div>
      ))}
      <button className="duo-btn outline" onClick={onBack}>‹ Back</button>
    </div>
  );
}

function QuizView({ me, onCorrectCountChange, showToast }) {
  const [stage, setStage] = useStateQuiz('track'); // 'track' | 'count' | 'quiz' | 'review'
  const [track, setTrack] = useStateQuiz(null);
  const [count, setCount] = useStateQuiz(null);
  const [difficulty, setDifficulty] = useStateQuiz('medium');
  const [reviewSession, setReviewSession] = useStateQuiz(null);
  const [runKey, setRunKey] = useStateQuiz(0); // bump to force a fresh QuizSession mount + refresh the sessions list

  const pickTrack = (t) => {
    setTrack(t);
    if (t.key === 'review') {
      setCount(0); // server decides review size
      setStage('quiz');
      setRunKey((k) => k + 1);
    } else {
      setStage('count');
    }
  };
  const pickCount = (c, d) => { setCount(c); setDifficulty(d || 'medium'); setStage('quiz'); setRunKey((k) => k + 1); };
  const restart = () => { setStage('track'); setTrack(null); setCount(null); setRunKey((k) => k + 1); };
  const openReview = (s) => { setReviewSession(s); setStage('review'); };

  let content;
  if (stage === 'track') content = <TrackSelect onPick={pickTrack} onBattle={() => setStage('battle')} />;
  else if (stage === 'battle') content = <BattleView onBack={restart} showToast={showToast} />;
  else if (stage === 'count') content = <CountSelect track={track} onPick={pickCount} onBack={() => setStage('track')} />;
  else if (stage === 'review') content = <QuizReview session={reviewSession} onBack={restart} />;
  else content = (
    <QuizSession
      key={runKey}
      track={track}
      count={count}
      difficulty={difficulty}
      onCorrectCountChange={onCorrectCountChange}
      showToast={showToast}
      onRestart={restart}
      onReview={openReview}
    />
  );

  return (
    <div className="quiz-page-grid">
      <div className="quiz-page-sidebar">
        <QuizHistoryPanel streak={me ? me.streak : 0} />
        <PastQuizzesPanel onReview={openReview} refreshKey={runKey} />
      </div>
      <div className="quiz-page-main">{content}</div>
    </div>
  );
}
