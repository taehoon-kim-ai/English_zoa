// ── section: main page — TED Talk of the day (hero) + team presence +
// condensed leaderboard. Pairs with tedtalk.go, profile.go's /api/team, and
// score.go's /api/leaderboard. Leaderboard no longer has its own screen —
// folded in here as a small sidebar widget per request.
const { useState: useStateMain, useEffect: useEffectMain } = React;

function medal(i) {
  return i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : i + 1;
}

function TedTalkCard() {
  const todayStr = new Date().toISOString().slice(0, 10);
  const [date, setDate] = useStateMain(todayStr);
  const [talk, setTalk] = useStateMain(null);
  const [error, setError] = useStateMain('');

  useEffectMain(() => {
    api(`/api/tedtalk?date=${date}`).then(setTalk).catch((e) => setError(e.message));
  }, [date]);

  if (error) return <div className="card state-msg">{error}</div>;
  if (!talk) return <div className="card state-msg">Loading today's talk...</div>;

  const strip = [];
  const center = new Date(date + 'T00:00:00');
  for (let i = 6; i >= 0; i--) {
    const d = new Date(center);
    d.setDate(d.getDate() - i);
    strip.push(d.toISOString().slice(0, 10));
  }

  const shift = (deltaDays) => {
    const d = new Date(date + 'T00:00:00');
    d.setDate(d.getDate() + deltaDays);
    const next = d.toISOString().slice(0, 10);
    setDate(next > todayStr ? todayStr : next);
  };

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
      <div className="tedtalk-strip">
        <button className="strip-arrow" onClick={() => shift(-7)}>‹</button>
        {strip.map((d) => (
          <button
            key={d}
            className={`strip-chip ${d === date ? 'active' : ''}`}
            onClick={() => d <= todayStr && setDate(d)}
            disabled={d > todayStr}
          >
            {d.slice(5)}
          </button>
        ))}
        <button className="strip-arrow" onClick={() => shift(7)} disabled={date >= todayStr}>›</button>
      </div>
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
      </div>
      <div className="main-col-sidebar">
        <TeamPanel />
        <MiniLeaderboard me={me} />
      </div>
    </div>
  );
}
