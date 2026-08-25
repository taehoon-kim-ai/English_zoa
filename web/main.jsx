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
            <div key={d} className={`strip-chip ${d === date ? 'active' : ''} ${d > todayStr ? 'future' : ''}`}>
              {d.slice(5)}
            </div>
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

function MainView({ me }) {
  return (
    <div className="main-grid">
      <div className="main-col-primary">
        <TedTalkCard />
        <NewsCard />
      </div>
      <div className="main-col-sidebar">
        <DailyGoalRing />
        <TeamPanel />
        <MiniLeaderboard me={me} />
      </div>
    </div>
  );
}
