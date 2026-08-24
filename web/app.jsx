const { useState, useEffect, useCallback, useRef } = React;

// ── api helper ────────────────────────────────────────────────────────────
async function api(path, opts) {
  const res = await fetch(path, {
    method: (opts && opts.method) || 'GET',
    headers: opts && opts.body ? { 'Content-Type': 'application/json' } : undefined,
    body: opts && opts.body ? JSON.stringify(opts.body) : undefined,
  });
  if (!res.ok) {
    const detail = await res.json().catch(() => ({}));
    throw new Error(detail.error || `HTTP ${res.status}`);
  }
  return res.json();
}

function useHashRoute() {
  const [route, setRoute] = useState(() => window.location.hash.replace('#', '') || 'home');
  useEffect(() => {
    const onHashChange = () => setRoute(window.location.hash.replace('#', '') || 'home');
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, []);
  return route;
}

// ── shared toast ──────────────────────────────────────────────────────────
function useToast() {
  const [msg, setMsg] = useState('');
  const timer = useRef(null);
  const show = useCallback((text) => {
    setMsg(text);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setMsg(''), 1800);
  }, []);
  const node = <div className={`toast ${msg ? 'show' : ''}`}>{msg}</div>;
  return [show, node];
}

// ── top bar ───────────────────────────────────────────────────────────────
function TopBar({ route, me }) {
  const tabs = [
    { key: 'home', label: '오늘의 문구' },
    { key: 'profile', label: '내 페이지' },
    { key: 'leaderboard', label: '리더보드' },
  ];
  return (
    <div className="topbar">
      <div className="topbar-brand">English<span>_zoa</span></div>
      <nav className="topbar-nav">
        {tabs.map((t) => (
          <a key={t.key} href={`#${t.key}`} className={route === t.key ? 'active' : ''}>{t.label}</a>
        ))}
      </nav>
      <div className="topbar-stats">
        {me && (
          <>
            <span className="pill">🔥 {me.streak}일 연속</span>
            <span className="pill">⭐ {me.score}점</span>
          </>
        )}
      </div>
    </div>
  );
}

// ── flashcard (home) view ────────────────────────────────────────────────
function HomeView({ me, onScoreChange, showToast }) {
  const [phrase, setPhrase] = useState(null);
  const [attempt, setAttempt] = useState('');
  const [flipped, setFlipped] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
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
      if (data.score_delta > 0) showToast(`+${data.score_delta}점!`);
      else if (data.score_delta < 0) showToast(`${data.score_delta}점`);
    } catch (e) {
      showToast(e.message);
    }
  };

  if (error) return <div className="state-msg">{error}</div>;
  if (!phrase) return <div className="state-msg">오늘의 문구를 불러오는 중...</div>;

  return (
    <div className="flashcard-wrap">
      {me && me.streak > 0 && (
        <div className="streak-banner">🔥 {me.streak}일 연속 접속 중이에요!</div>
      )}
      <div className="flashcard-scene" onClick={() => setFlipped((f) => !f)}>
        <div className={`flashcard ${flipped ? 'flipped' : ''}`}>
          <div className="flashcard-face front">{phrase.english_text}</div>
          <div className="flashcard-face back">{phrase.korean_text}</div>
        </div>
      </div>
      <div className="flashcard-hint">카드를 클릭하면 뒤집혀요</div>
      <div className="answer-buttons">
        <button className={`know ${attempt === 'known' ? 'active' : ''}`} onClick={() => answer('known')}>
          알아요
        </button>
        <button className={`dont-know ${attempt === 'unknown' ? 'active' : ''}`} onClick={() => answer('unknown')}>
          몰라요
        </button>
      </div>
    </div>
  );
}

// ── profile view ─────────────────────────────────────────────────────────
function ProfileView({ me, showToast }) {
  const [nickname, setNickname] = useState('');
  const [statusMessage, setStatusMessage] = useState('');
  const [events, setEvents] = useState([]);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (me) {
      setNickname(me.nickname);
      setStatusMessage(me.status_message);
    }
  }, [me]);

  useEffect(() => {
    api('/api/calendar')
      .then((data) => setEvents(data.events || []))
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, []);

  const save = async () => {
    try {
      await api('/api/profile', { method: 'POST', body: { nickname, status_message: statusMessage } });
      showToast('저장했어요');
    } catch (e) {
      showToast(e.message);
    }
  };

  const eventByDate = {};
  events.forEach((e) => { eventByDate[e.date] = e; });

  const days = [];
  const today = new Date();
  for (let i = 41; i >= 0; i--) {
    const d = new Date(today);
    d.setDate(d.getDate() - i);
    const key = d.toISOString().slice(0, 10);
    days.push({ key, dayNum: d.getDate(), event: eventByDate[key] });
  }

  return (
    <div>
      <div className="card">
        <div className="profile-field">
          <label>닉네임</label>
          <input value={nickname} onChange={(e) => setNickname(e.target.value)} maxLength={24} />
        </div>
        <div className="profile-field">
          <label>상태 메시지</label>
          <input value={statusMessage} onChange={(e) => setStatusMessage(e.target.value)} maxLength={80} />
        </div>
        <button className="profile-save" onClick={save}>저장</button>
      </div>

      <div className="card" style={{ marginTop: 20 }}>
        <label style={{ fontSize: 13, fontWeight: 700, color: 'var(--text-muted)' }}>최근 6주 접속 기록</label>
        {loaded && (
          <div className="calendar-grid">
            {days.map((d) => (
              <div key={d.key} className={`calendar-cell ${d.event ? 'logged-in' : ''}`} title={d.event ? `${d.key} ${d.event.time} 접속` : d.key}>
                <span className="day-num">{d.dayNum}</span>
                {d.event ? d.event.time : ''}
              </div>
            ))}
          </div>
        )}
        <div className="calendar-legend">
          <span className="calendar-cell logged-in" style={{ width: 14, height: 14, aspectRatio: 'auto', display: 'inline-block' }} />
          접속한 날 (표시된 시각 = 그날 첫 접속 시각)
        </div>
      </div>
    </div>
  );
}

// ── leaderboard view ─────────────────────────────────────────────────────
function LeaderboardView({ me }) {
  const [rows, setRows] = useState(null);
  const [error, setError] = useState('');

  useEffect(() => {
    api('/api/leaderboard')
      .then((data) => setRows(data.leaderboard || []))
      .catch((e) => setError(e.message));
  }, []);

  if (error) return <div className="state-msg">{error}</div>;
  if (!rows) return <div className="state-msg">불러오는 중...</div>;

  const rankClass = (i) => (i === 0 ? 'top1' : i === 1 ? 'top2' : i === 2 ? 'top3' : '');

  return (
    <div className="card">
      <table className="leaderboard-table">
        <thead>
          <tr><th>순위</th><th>닉네임</th><th>점수</th></tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.email} className={me && r.email === me.email ? 'me' : ''}>
              <td><span className={`rank-badge ${rankClass(i)}`}>{i + 1}</span></td>
              <td>{r.nickname}</td>
              <td>{r.total_score}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {rows.length === 0 && <div className="state-msg">아직 아무도 점수가 없어요 — 오늘의 문구부터 풀어보세요!</div>}
    </div>
  );
}

// ── app shell ────────────────────────────────────────────────────────────
function App() {
  const route = useHashRoute();
  const [me, setMe] = useState(null);
  const [showToast, toastNode] = useToast();

  const loadMe = useCallback(() => {
    api('/api/me').then(setMe).catch(() => {});
  }, []);

  useEffect(() => { loadMe(); }, [loadMe]);

  const onScoreChange = (score) => setMe((prev) => (prev ? { ...prev, score } : prev));

  let view;
  if (route === 'profile') view = <ProfileView me={me} showToast={showToast} />;
  else if (route === 'leaderboard') view = <LeaderboardView me={me} />;
  else view = <HomeView me={me} onScoreChange={onScoreChange} showToast={showToast} />;

  return (
    <>
      <TopBar route={route === 'profile' || route === 'leaderboard' ? route : 'home'} me={me} />
      <main>{view}</main>
      {toastNode}
    </>
  );
}
