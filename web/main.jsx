// ── section: main page — TED Talk of the day (hero) + team presence +
// condensed leaderboard. Pairs with tedtalk.go, profile.go's /api/team, and
// score.go's /api/leaderboard. Leaderboard no longer has its own screen —
// folded in here as a small sidebar widget per request.
const { useState: useStateMain, useEffect: useEffectMain } = React;

function medal(i) {
  return i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : i + 1;
}

// dateStr/addDays are plain helpers for local calendar-date math. Built from
// local getters, NOT toISOString() — toISOString() converts to UTC, which
// silently shifts the date by one whenever the browser's local timezone is
// ahead of UTC (e.g. Asia/Seoul, UTC+9): local midnight becomes the previous
// day's evening in UTC. That bug made a single "back one day" arrow click
// jump back two days.
function dateStr(d) {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}
function addDays(str, delta) {
  const d = new Date(str + 'T00:00:00');
  d.setDate(d.getDate() + delta);
  return dateStr(d);
}

// Reusable center-anchored date strip: the selected date sits in the middle
// chip, arrows step one day with a slide animation, future dates disabled.
// Used by both the TED Talk and Daily News cards.
function DateStrip({ date, todayStr, onChange }) {
  const [direction, setDirection] = useStateMain('none');
  const strip = [-2, -1, 0, 1, 2].map((offset) => addDays(date, offset));
  const shift = (delta) => {
    setDirection(delta < 0 ? 'right' : 'left');
    const next = addDays(date, delta);
    onChange(next > todayStr ? todayStr : next);
  };
  return (
    <div className="tedtalk-strip">
      <button className="strip-arrow" onClick={() => shift(-1)}>‹</button>
      <div className="strip-window">
        <div key={date} className={`strip-track slide-${direction}`}>
          {strip.map((d) => (
            <button
              key={d}
              className={`strip-chip ${d === date ? 'active' : ''} ${d > todayStr ? 'future' : ''}`}
              onClick={() => { if (d <= todayStr && d !== date) { setDirection(d < date ? 'right' : 'left'); onChange(d); } }}
              disabled={d > todayStr}
            >
              {d.slice(5)}
            </button>
          ))}
        </div>
      </div>
      <button className="strip-arrow" onClick={() => shift(1)} disabled={date >= todayStr}>›</button>
    </div>
  );
}

function TedTalkCard() {
  const todayStr = dateStr(new Date());
  const [date, setDate] = useStateMain(todayStr);
  const [talk, setTalk] = useStateMain(null);
  const [error, setError] = useStateMain('');

  useEffectMain(() => {
    api(`/api/tedtalk?date=${date}`).then(setTalk).catch((e) => setError(e.message));
  }, [date]);

  if (error) return <div className="card state-msg">{error}</div>;
  if (!talk) return <div className="card state-msg">Loading today's talk...</div>;

  return (
    <div className="card tedtalk-card">
      <div className="tagline">🎤 TED Talk of the Day</div>
      <div className="tedtalk-player">
        <iframe
          src={`https://www.youtube.com/embed/${talk.video_id}`}
          title={talk.title}
          allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
          allowFullScreen
        />
      </div>
      <div className="tedtalk-meta">
        <div className="tedtalk-title">{talk.title}</div>
        <div className="tedtalk-speaker">{talk.speaker} · {date}</div>
      </div>
      <DateStrip date={date} todayStr={todayStr} onChange={setDate} />
      <TedTalkComments videoId={talk.video_id} />
    </div>
  );
}

// Comments are keyed by video_id (not date) so a repeat airing of the same
// talk reopens the same discussion thread instead of starting a fresh one.
function TedTalkComments({ videoId }) {
  const [comments, setComments] = useStateMain(null);
  const [draft, setDraft] = useStateMain('');
  const [posting, setPosting] = useStateMain(false);

  useEffectMain(() => {
    setComments(null);
    api(`/api/tedtalk/comments?video_id=${encodeURIComponent(videoId)}`)
      .then((data) => setComments(data.comments || []))
      .catch(() => setComments([]));
  }, [videoId]);

  const post = async () => {
    const text = draft.trim();
    if (!text) return;
    setPosting(true);
    try {
      const data = await api('/api/tedtalk/comments', { method: 'POST', body: { video_id: videoId, body: text } });
      setComments(data.comments || []);
      setDraft('');
    } catch (e) {
      // best-effort — leave the draft in place so the user can retry
    } finally {
      setPosting(false);
    }
  };

  return (
    <div className="tedtalk-comments">
      <div className="mini-lb-title">💭 Discuss this talk</div>
      {comments === null && <div className="state-msg">Loading comments...</div>}
      {comments && comments.length === 0 && <div className="mini-lb-empty">No comments yet — start the discussion!</div>}
      {comments && comments.map((c) => (
        <div key={c.id} className="comment-row">
          <div className="comment-meta"><span className="comment-name">{c.nickname}</span> · {c.created_at}</div>
          <div className="comment-body">{c.body}</div>
        </div>
      ))}
      <div className="comment-form">
        <textarea
          className="translate-input"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="Share your thoughts in English..."
          rows={2}
        />
        <button className="duo-btn blue" onClick={post} disabled={posting || !draft.trim()}>
          {posting ? 'Posting...' : 'Post'}
        </button>
      </div>
    </div>
  );
}

// One SVG donut for a single track's daily progress.
function GoalRing({ label, icon, answered, goal }) {
  const pct = Math.min(answered / goal, 1);
  const R = 42;
  const CIRC = 2 * Math.PI * R;
  const done = answered >= goal;
  return (
    <div className="goal-ring-block">
      <div className="goal-ring-wrap small">
        <svg viewBox="0 0 100 100" className="goal-ring">
          <circle cx="50" cy="50" r={R} className="goal-ring-track" />
          <circle
            cx="50" cy="50" r={R}
            className={`goal-ring-fill ${done ? 'done' : ''}`}
            strokeDasharray={CIRC}
            strokeDashoffset={CIRC * (1 - pct)}
          />
        </svg>
        <div className="goal-ring-center">
          <div className="goal-ring-count">{answered}<span className="goal-ring-total">/{goal}</span></div>
        </div>
      </div>
      <div className="goal-ring-label">{icon} {label}{done ? ' 🎉' : ''}</div>
    </div>
  );
}

// Today's per-track goals (badges.go /api/stats) — each track has its own
// user-set target, editable on the profile page.
function DailyGoalRing() {
  const [goal, setGoal] = useStateMain(null);

  useEffectMain(() => {
    api('/api/stats').then((data) => setGoal(data.daily_goal)).catch(() => {});
  }, []);

  if (!goal) return null;

  const allDone = goal.vocab.answered >= goal.vocab.goal && goal.phrase.answered >= goal.phrase.goal;

  return (
    <div className="card goal-card">
      <div className="tagline" style={{ marginBottom: 8 }}>⚡ Today's Goals</div>
      <div className="goal-rings-row">
        <GoalRing label="Vocab" icon="🔤" answered={goal.vocab.answered} goal={goal.vocab.goal} />
        <GoalRing label="Phrase" icon="💬" answered={goal.phrase.answered} goal={goal.phrase.goal} />
      </div>
      {!allDone && <a href="#quiz" className="goal-cta">Take a quiz →</a>}
    </div>
  );
}

// Daily English business-news story (news.go) with date navigation — one
// story is archived per day, so past days are browsable like the TED Talk.
// Layout: thumbnail + source link on the left, EN/KO summaries on the right.
function NewsCard() {
  const todayStr = dateStr(new Date());
  const [date, setDate] = useStateMain(todayStr);
  const [story, setStory] = useStateMain(null);

  useEffectMain(() => {
    setStory(null);
    api(`/api/news?date=${date}`).then(setStory).catch(() => setStory({ missing: true, date }));
  }, [date]);

  return (
    <div className="card news-card">
      <div className="tagline" style={{ marginBottom: 8 }}>📰 Daily Business News</div>

      {!story && <div className="state-msg">Loading...</div>}
      {story && story.missing && (
        <div className="state-msg">No news archived for {story.date} — stories are saved from the day the app started.</div>
      )}
      {story && !story.missing && (
        <>
          <div className="news-title">{story.title}</div>
          <div className="news-grid">
            <div className="news-left">
              {story.image_url && <img className="news-thumb" src={story.image_url} alt="" />}
              <a className="news-link" href={story.url} target="_blank" rel="noopener noreferrer">
                Read on {story.source} →
              </a>
            </div>
            <div className="news-right">
              {story.summary && (
                <div className="news-summary">
                  <span className="news-lang-tag">EN</span> {story.summary}
                </div>
              )}
              {story.summary_ko && (
                <div className="news-summary ko">
                  <span className="news-lang-tag">KO</span> {story.summary_ko}
                </div>
              )}
            </div>
          </div>
        </>
      )}

      <DateStrip date={date} todayStr={todayStr} onChange={setDate} />
    </div>
  );
}

function TeamPanel() {
  const [team, setTeam] = useStateMain(null);

  useEffectMain(() => {
    api('/api/team').then((data) => setTeam(data.members || [])).catch(() => {});
  }, []);

  if (!team) return null;

  return (
    <div className="card">
      <div className="tagline">👥 Team</div>
      {team.map((m) => (
        <div key={m.email} className="team-row">
          <span className={`presence-dot ${m.online ? 'online' : ''}`} title={m.online ? 'Online now' : 'Offline'} />
          <div className="team-info">
            <div className="team-name">{m.nickname}</div>
            {m.status_message && <div className="team-status">{m.status_message}</div>}
          </div>
        </div>
      ))}
      {team.length === 0 && <div className="state-msg">No teammates yet.</div>}
    </div>
  );
}

function MiniLeaderboard({ me }) {
  const [data, setData] = useStateMain(null);

  useEffectMain(() => {
    api('/api/leaderboard').then(setData).catch(() => {});
  }, []);

  if (!data) return null;

  const section = (title, rows, valueKey) => (
    <div className="mini-lb-section">
      <div className="mini-lb-title">{title}</div>
      {rows.slice(0, 3).map((r, i) => (
        <div key={r.email} className={`mini-lb-row ${me && r.email === me.email ? 'me' : ''}`}>
          <span className="mini-lb-rank">{medal(i)}</span>
          <span className="mini-lb-name">{r.nickname}</span>
          <span className="mini-lb-value">{r[valueKey]}</span>
        </div>
      ))}
      {rows.length === 0 && <div className="mini-lb-empty">No data yet</div>}
    </div>
  );

  return (
    <div className="card">
      <div className="tagline">🏆 Leaderboard</div>
      {section('🎯 Most Correct', data.quiz || [], 'correct_count')}
      {section('🔥 Streak This Month', data.streak || [], 'best_streak')}
    </div>
  );
}

function WeeklyArenaCard({ weekly }) {
  if (!weekly) return null;
  const live = weekly.live || {};
  const rows = (items, render) => (
    (items || []).slice(0, 3).map((r, i) => (
      <div key={r.nickname + i} className="mini-lb-row">
        <span className="mini-lb-rank">{medal(i)}</span>
        <span className="mini-lb-name">{r.nickname}</span>
        <span className="mini-lb-value">{render(r)}</span>
      </div>
    ))
  );
  const empty = <div className="mini-lb-empty">No data yet</div>;
  return (
    <div className="card">
      <div className="tagline">⚔️ Weekly Arena</div>
      <div className="weekly-note">Resets every Friday 9:00 AM · champions crowned on Fridays</div>
      <div className="mini-lb-section">
        <div className="mini-lb-title">🎮 Battle Record</div>
        {(live.battle || []).length ? rows(live.battle, (r) => `${r.wins}W–${r.losses}L`) : empty}
      </div>
      <div className="mini-lb-section">
        <div className="mini-lb-title">🔥 Best Streak</div>
        {(live.streak || []).length ? rows(live.streak, (r) => r.value) : empty}
      </div>
      <div className="mini-lb-section">
        <div className="mini-lb-title">📚 Words Solved</div>
        {(live.words || []).length ? rows(live.words, (r) => r.value) : empty}
      </div>
    </div>
  );
}

// Full-screen Friday-morning celebration: shown once per user per week
// (dismissal remembered in localStorage keyed by the celebrated week).
function CelebrationOverlay({ celebration }) {
  const [visible, setVisible] = useStateMain(false);

  useEffectMain(() => {
    if (!celebration) return;
    try {
      if (localStorage.getItem('phraseup-celebrated-' + celebration.week_of)) return;
    } catch (e) { /* storage unavailable — still celebrate */ }
    setVisible(true);
  }, [celebration && celebration.week_of]);

  if (!celebration || !visible) return null;

  const dismiss = () => {
    try { localStorage.setItem('phraseup-celebrated-' + celebration.week_of, '1'); } catch (e) {}
    setVisible(false);
  };

  const c = celebration.champions || {};
  const podium = [
    c.battle && { icon: '🎮', title: 'Battle Champion', name: c.battle.nickname, detail: `${c.battle.wins}W–${c.battle.losses}L · ${Math.round((c.battle.win_rate || 0) * 100)}% win rate` },
    c.streak && { icon: '🔥', title: 'Streak Champion', name: c.streak.nickname, detail: `${c.streak.value}-day streak` },
    c.words && { icon: '📚', title: 'Word Champion', name: c.words.nickname, detail: `${c.words.value} words solved` },
  ].filter(Boolean);

  const pieces = Array.from({ length: 120 }, (_, i) => (
    <span
      key={i}
      className="celebrate-confetti"
      style={{
        left: (i * 137.5) % 100 + '%',
        animationDelay: (i % 20) * 0.15 + 's',
        animationDuration: 2.5 + (i % 7) * 0.4 + 's',
        background: ['#58cc02', '#1cb0f6', '#ff9600', '#ff4b4b', '#ce82ff', '#ffd900'][i % 6],
      }}
    />
  ));

  return (
    <div className="celebrate-overlay" onClick={dismiss}>
      {pieces}
      <div className="celebrate-box" onClick={(e) => e.stopPropagation()}>
        <div className="celebrate-title">🏆 Champions of the Week 🏆</div>
        <div className="celebrate-week">Week of {celebration.week_of}</div>
        <div className="celebrate-podium">
          {podium.map((p) => (
            <div key={p.title} className="celebrate-champ">
              <div className="celebrate-champ-icon">{p.icon}</div>
              <div className="celebrate-champ-title">{p.title}</div>
              <div className="celebrate-champ-name">{p.name}</div>
              <div className="celebrate-champ-detail">{p.detail}</div>
            </div>
          ))}
        </div>
        <button className="duo-btn" onClick={dismiss}>Awesome! 🎉</button>
      </div>
    </div>
  );
}

function MainView({ me }) {
  const [weekly, setWeekly] = useStateMain(null);

  useEffectMain(() => {
    api('/api/weekly').then(setWeekly).catch(() => {});
  }, []);

  return (
    <div className="main-grid">
      <CelebrationOverlay celebration={weekly && weekly.celebration} />
      <div className="main-col-primary">
        <TedTalkCard />
        <NewsCard />
      </div>
      <div className="main-col-sidebar">
        <DailyGoalRing />
        <WeeklyArenaCard weekly={weekly} />
        <TeamPanel />
        <MiniLeaderboard me={me} />
      </div>
    </div>
  );
}
