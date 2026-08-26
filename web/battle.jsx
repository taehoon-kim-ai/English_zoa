// ── section: word battle — pairs with battle.go. Team lobby (name your
// teams, pick left/right sides, host starts), 60-second team typing races
// with per-miss letter hints, and team battle-tetris where a correct vocab
// answer (keys 1-4) unlocks 5 seconds of free play and cleared lines send
// every living opponent a one-gap garbage line. All sync is 1s polling.
const { useState: useStateBattle, useEffect: useEffectBattle, useRef: useRefBattle, useCallback: useCallbackBattle } = React;

const BATTLE_GAMES = [
  { key: 'word', icon: '🔤', title: 'Word Race', desc: 'Teams · 60 seconds · misses earn hint letters' },
  { key: 'phrase', icon: '💬', title: 'Phrase Race', desc: 'Teams · 60 seconds · type the sentence first' },
  { key: 'tetris', icon: '🧱', title: 'Word Tetris', desc: 'Teams · answer (1-4) → 5s of play · clears attack' },
];

const GAME_ICON = { word: '🔤', phrase: '💬', tetris: '🧱' };

// Smooth local clock: each poll re-anchors "server says N ms left" to
// Date.now(), and a 100ms ticker renders between polls.
function useLocalCountdown(serverMs) {
  const anchorRef = useRefBattle(0);
  const [, setTick] = useStateBattle(0);
  useEffectBattle(() => {
    anchorRef.current = Date.now() + (serverMs || 0);
  }, [serverMs]);
  useEffectBattle(() => {
    const iv = setInterval(() => setTick((t) => t + 1), 100);
    return () => clearInterval(iv);
  }, []);
  return Math.max(0, anchorRef.current - Date.now());
}

// The 3‥2‥1‥START! overlay. Shows big digits while ms > 0 and flashes
// START! for a beat right after the clock crosses zero.
function CountdownOverlay({ ms }) {
  const [showStart, setShowStart] = useStateBattle(false);
  const wasCounting = useRefBattle(false);
  useEffectBattle(() => {
    if (ms > 0) { wasCounting.current = true; return; }
    if (wasCounting.current) {
      wasCounting.current = false;
      setShowStart(true);
      const t = setTimeout(() => setShowStart(false), 700);
      return () => clearTimeout(t);
    }
  }, [ms > 0]);
  if (ms > 0) return <div className="battle-countdown big" key={Math.ceil(ms / 1000)}>{Math.ceil(ms / 1000)}</div>;
  if (showStart) return <div className="battle-countdown start">START!</div>;
  return null;
}

// ── tetris engine ─────────────────────────────────────────────────────────
const TET_W = 10, TET_H = 20;
const TET_SHAPES = {
  I: [[1, 1, 1, 1]],
  O: [[1, 1], [1, 1]],
  T: [[1, 1, 1], [0, 1, 0]],
  S: [[0, 1, 1], [1, 1, 0]],
  Z: [[1, 1, 0], [0, 1, 1]],
  J: [[1, 0, 0], [1, 1, 1]],
  L: [[0, 0, 1], [1, 1, 1]],
};
const TET_COLORS = { I: '#006CFA', O: '#FDB022', T: '#7C4DFF', S: '#12B76A', Z: '#F04438', J: '#0059D6', L: '#F79009', G: '#9aa0aa' };
const TET_KEYS = Object.keys(TET_SHAPES);
const TET_FREE_PLAY_MS = 5000; // one correct answer buys this much play time

function tetRotate(shape) {
  const rows = shape.length, cols = shape[0].length;
  const out = Array.from({ length: cols }, () => Array(rows).fill(0));
  for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) out[c][rows - 1 - r] = shape[r][c];
  return out;
}

function tetCollides(grid, shape, x, y) {
  for (let r = 0; r < shape.length; r++) {
    for (let c = 0; c < shape[r].length; c++) {
      if (!shape[r][c]) continue;
      const gx = x + c, gy = y + r;
      if (gx < 0 || gx >= TET_W || gy >= TET_H) return true;
      if (gy >= 0 && grid[gy][gx]) return true;
    }
  }
  return false;
}

function TetrisGame({ battleId, state, showToast }) {
  // The board lives in refs (mutated by the game loop); a tick counter
  // state forces re-render.
  const gridRef = useRefBattle(Array.from({ length: TET_H }, () => Array(TET_W).fill(null)));
  const pieceRef = useRefBattle(null); // { key, shape, x, y }
  const [, setTick] = useStateBattle(0);
  const [mode, setMode] = useStateBattle('gate'); // 'gate' | 'playing' | 'dead'
  const [gate, setGate] = useStateBattle(null);   // { korean, options, sig }
  const [gateFlash, setGateFlash] = useStateBattle('');
  const consumedRef = useRefBattle(false);
  const modeRef = useRefBattle('gate');
  const freeUntilRef = useRefBattle(0); // free-play window deadline
  const gateRef = useRefBattle(null);
  modeRef.current = mode;
  gateRef.current = gate;

  const rerender = () => setTick((t) => t + 1);

  const fetchGate = useCallbackBattle(() => {
    api(`/api/battle/${battleId}/tetris/question`).then(setGate).catch(() => {});
  }, [battleId]);

  useEffectBattle(() => { fetchGate(); }, [fetchGate]);

  const spawn = () => {
    const key = TET_KEYS[Math.floor(Math.random() * TET_KEYS.length)];
    const shape = TET_SHAPES[key].map((r) => [...r]);
    const piece = { key, shape, x: Math.floor((TET_W - shape[0].length) / 2), y: 0 };
    if (tetCollides(gridRef.current, piece.shape, piece.x, piece.y)) {
      setMode('dead');
      api('/api/battle/tetris/gameover', { method: 'POST', body: { battle_id: battleId } }).catch(() => {});
      return;
    }
    pieceRef.current = piece;
    setMode('playing');
    rerender();
  };

  const lockPiece = () => {
    const p = pieceRef.current;
    if (!p) return;
    for (let r = 0; r < p.shape.length; r++)
      for (let c = 0; c < p.shape[r].length; c++)
        if (p.shape[r][c] && p.y + r >= 0) gridRef.current[p.y + r][p.x + c] = p.key;
    pieceRef.current = null;

    // clear full lines
    let cleared = 0;
    gridRef.current = gridRef.current.filter((row) => {
      if (row.every((cell) => cell)) { cleared++; return false; }
      return true;
    });
    while (gridRef.current.length < TET_H) gridRef.current.unshift(Array(TET_W).fill(null));
    if (cleared > 0) {
      api('/api/battle/tetris/lines', { method: 'POST', body: { battle_id: battleId, lines: cleared } }).catch(() => {});
    }
    // still inside the free-play window? next piece is free — otherwise a
    // fresh question gates it
    if (Date.now() < freeUntilRef.current) {
      spawn();
    } else {
      setMode('gate');
      fetchGate();
    }
    rerender();
  };

  const move = (dx, dy) => {
    const p = pieceRef.current;
    if (!p || modeRef.current !== 'playing') return false;
    if (!tetCollides(gridRef.current, p.shape, p.x + dx, p.y + dy)) {
      p.x += dx; p.y += dy; rerender(); return true;
    }
    if (dy === 1) lockPiece();
    return false;
  };

  const rotate = () => {
    const p = pieceRef.current;
    if (!p || modeRef.current !== 'playing') return;
    const rotated = tetRotate(p.shape);
    if (!tetCollides(gridRef.current, rotated, p.x, p.y)) { p.shape = rotated; rerender(); }
  };

  const hardDrop = () => {
    const p = pieceRef.current;
    if (!p || modeRef.current !== 'playing') return;
    while (tetCollides(gridRef.current, p.shape, p.x, p.y + 1) === false) p.y++;
    lockPiece();
  };

  const answerGate = async (english) => {
    const g = gateRef.current;
    if (!g) return;
    try {
      const d = await api('/api/battle/tetris/gate', { method: 'POST', body: { battle_id: battleId, english, sig: g.sig } });
      if (d.correct) {
        setGate(null);
        setGateFlash('');
        freeUntilRef.current = Date.now() + TET_FREE_PLAY_MS;
        spawn();
      } else {
        setGateFlash('❌ Wrong — new question!');
        setTimeout(() => { setGateFlash(''); fetchGate(); }, 700);
      }
    } catch (e) { showToast(e.message); }
  };

  // gravity + free-play countdown repaint
  useEffectBattle(() => {
    const iv = setInterval(() => { if (modeRef.current === 'playing') move(0, 1); }, 650);
    const paint = setInterval(() => { if (modeRef.current === 'playing') rerender(); }, 250);
    return () => { clearInterval(iv); clearInterval(paint); };
  }, []);

  // keyboard: arrows/space drive the board; 1-4 answer the gate question
  useEffectBattle(() => {
    const onKey = (e) => {
      if (modeRef.current === 'gate') {
        const g = gateRef.current;
        const n = parseInt(e.key, 10);
        if (g && n >= 1 && n <= (g.options || []).length) {
          e.preventDefault();
          answerGate(g.options[n - 1].english);
        }
        return;
      }
      if (modeRef.current !== 'playing') return;
      if (e.key === 'ArrowLeft') { e.preventDefault(); move(-1, 0); }
      else if (e.key === 'ArrowRight') { e.preventDefault(); move(1, 0); }
      else if (e.key === 'ArrowDown') { e.preventDefault(); move(0, 1); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); rotate(); }
      else if (e.key === ' ') { e.preventDefault(); hardDrop(); }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // incoming garbage: rows pushed from the bottom, each with ONE random gap
  useEffectBattle(() => {
    const pending = state.my_pending_garbage || 0;
    if (pending > 0 && !consumedRef.current && modeRef.current !== 'dead') {
      consumedRef.current = true;
      for (let i = 0; i < pending; i++) {
        const gap = Math.floor(Math.random() * TET_W);
        const row = Array(TET_W).fill('G');
        row[gap] = null;
        gridRef.current.shift();
        gridRef.current.push(row);
      }
      rerender();
      api('/api/battle/tetris/consume', { method: 'POST', body: { battle_id: battleId, lines: pending } })
        .finally(() => { consumedRef.current = false; });
    }
  }, [state.my_pending_garbage, battleId]);

  // compose render grid (board + falling piece)
  const cells = gridRef.current.map((row) => [...row]);
  const p = pieceRef.current;
  if (p) {
    for (let r = 0; r < p.shape.length; r++)
      for (let c = 0; c < p.shape[r].length; c++)
        if (p.shape[r][c] && p.y + r >= 0) cells[p.y + r][p.x + c] = p.key;
  }

  const freeLeft = Math.max(0, freeUntilRef.current - Date.now());
  const teamRows = (team) => (team.players || []).map((pl) => (
    <div key={pl.nickname} className={`tetris-stat ${pl.dead ? 'dead' : ''}`}>
      <span>{pl.dead ? '💀 ' : ''}{pl.nickname}</span><b>{pl.lines} lines</b>
    </div>
  ));

  return (
    <div className="tetris-wrap">
      <div className="tetris-board" tabIndex={0}>
        {cells.map((row, r) => (
          <div key={r} className="tetris-row">
            {row.map((cell, c) => (
              <div key={c} className="tetris-cell" style={cell ? { background: TET_COLORS[cell] } : undefined} />
            ))}
          </div>
        ))}
        {mode === 'playing' && freeLeft > 0 && (
          <div className="tetris-freeplay">▶ {(freeLeft / 1000).toFixed(1)}s</div>
        )}
        {mode === 'gate' && gate && (
          <div className="tetris-gate">
            <div className="tetris-gate-title">Answer to unlock 5 seconds of play</div>
            <div className="tetris-gate-korean">{gate.korean}</div>
            <div className="tetris-gate-options">
              {gate.options.map((o, i) => (
                <button key={o.english} className="quiz-option" onClick={() => answerGate(o.english)}>
                  <span className="tetris-gate-num">{i + 1}</span> {o.english}
                </button>
              ))}
            </div>
            <div className="tetris-help">Press 1-4 to answer</div>
            {gateFlash && <div className="battle-flash">{gateFlash}</div>}
          </div>
        )}
        {mode === 'dead' && (
          <div className="tetris-gate">
            <div className="tetris-gate-title">💀 Topped out</div>
            <div className="tetris-help">Your teammates fight on — hang tight</div>
          </div>
        )}
      </div>
      <div className="tetris-side">
        <div className="tetris-team-title">{state.team_left.name} · {state.team_left.score}</div>
        {teamRows(state.team_left)}
        <div className="tetris-team-title right">{state.team_right.name} · {state.team_right.score}</div>
        {teamRows(state.team_right)}
        <div className="tetris-help">← → move · ↑ rotate · ↓ soft drop · Space hard drop</div>
        <div className="tetris-help">Clear a line → every opponent gets a one-gap garbage line</div>
      </div>
    </div>
  );
}

// ── team lobby (waiting room) ─────────────────────────────────────────────
function TeamLobbyView({ state, battleId, showToast, onLeave }) {
  const [names, setNames] = useStateBattle({ left: null, right: null });
  const [team, setTeam] = useStateBattle([]);
  const [invited, setInvited] = useStateBattle({}); // email → true (optimistic)

  useEffectBattle(() => {
    api('/api/team').then((d) => setTeam(d.members || d || [])).catch(() => {});
  }, []);

  const invite = async (email) => {
    setInvited((m) => ({ ...m, [email]: true }));
    try {
      await api('/api/battle/invite', { method: 'POST', body: { battle_id: battleId, email } });
    } catch (e) {
      setInvited((m) => ({ ...m, [email]: false }));
      showToast(e.message);
    }
  };

  const seated = new Set([...state.team_left.players, ...state.team_right.players].map((p) => p.nickname));
  const invitedNames = new Set(state.invited_names || []);
  const invitable = team.filter((m) => !seated.has(m.nickname));

  const saveName = async (side) => {
    const val = names[side];
    if (val == null || !val.trim()) return;
    try {
      await api('/api/battle/team-name', { method: 'POST', body: { battle_id: battleId, side, name: val.trim() } });
    } catch (e) { showToast(e.message); }
    setNames((n) => ({ ...n, [side]: null }));
  };

  const switchTo = async (team) => {
    try {
      if (state.my_team) await api('/api/battle/side', { method: 'POST', body: { battle_id: battleId, team } });
      else await api('/api/battle/join', { method: 'POST', body: { battle_id: battleId, team } });
    } catch (e) { showToast(e.message); }
  };

  const start = async () => {
    try {
      await api('/api/battle/start', { method: 'POST', body: { battle_id: battleId } });
    } catch (e) { showToast(e.message); }
  };

  const canStart = state.team_left.players.length > 0 && state.team_right.players.length > 0;

  const teamPanel = (side, team) => (
    <div className={`team-panel ${side} ${state.my_team === side ? 'mine' : ''}`}>
      {state.is_host ? (
        <input
          className="team-name-input"
          value={names[side] != null ? names[side] : team.name}
          maxLength={24}
          onChange={(e) => setNames((n) => ({ ...n, [side]: e.target.value }))}
          onBlur={() => saveName(side)}
          onKeyDown={(e) => { if (e.key === 'Enter') e.target.blur(); }}
        />
      ) : (
        <div className="team-name">{team.name}</div>
      )}
      <div className="team-players">
        {team.players.map((p) => (
          <div key={p.nickname} className="team-player">
            {p.avatar ? <img className="player-avatar" src={p.avatar} alt="" /> : <span className="player-avatar fallback">{p.nickname.charAt(0).toUpperCase()}</span>}
            {p.nickname}
          </div>
        ))}
        {team.players.length === 0 && <div className="team-player empty">Waiting for players…</div>}
      </div>
      {state.my_team !== side && (
        <button className="duo-btn outline small" onClick={() => switchTo(side)}>
          {state.my_team ? 'Switch here' : 'Join this side'}
        </button>
      )}
    </div>
  );

  return (
    <div className="quiz-wrap">
      <div className="tagline">{GAME_ICON[state.game_type]} Team Lobby — {state.host_name}'s match</div>
      <div className="card battle-card team-lobby">
        <div className="team-lobby-grid">
          {teamPanel('left', state.team_left)}
          <div className="team-vs">VS</div>
          {teamPanel('right', state.team_right)}
        </div>
        {state.is_host && <div className="battle-sub">You're the host — name the teams, then hit Start</div>}
        {invitable.length > 0 && (
          <div className="invite-panel">
            <div className="mini-lb-title">📨 Invite teammates</div>
            {invitable.map((m) => {
              const sent = invited[m.email] || invitedNames.has(m.nickname);
              return (
                <div key={m.email} className="invite-row">
                  {m.avatar_url ? <img className="player-avatar" src={m.avatar_url} alt="" /> : <span className="player-avatar fallback">{m.nickname.charAt(0).toUpperCase()}</span>}
                  <span className="invite-name">{m.nickname}</span>
                  {m.online && <span className="invite-online">● online</span>}
                  {sent
                    ? <span className="invite-sent">Invited ✓</span>
                    : <button className="duo-btn blue small" onClick={() => invite(m.email)}>Invite</button>}
                </div>
              );
            })}
          </div>
        )}
        <div className="quiz-complete-actions">
          {state.is_host && (
            <button className="duo-btn" disabled={!canStart} onClick={start}>
              {canStart ? '🚀 Start Match' : 'Need players on both sides'}
            </button>
          )}
          <button className="duo-btn outline" onClick={onLeave}>{state.is_host ? 'Cancel Match' : 'Leave'}</button>
        </div>
      </div>
    </div>
  );
}

// ── battle shell ──────────────────────────────────────────────────────────
function BattleView({ onBack, showToast }) {
  const [battleId, setBattleId] = useStateBattle(null);
  const [spectating, setSpectating] = useStateBattle(false);
  const [lobby, setLobby] = useStateBattle(null);
  const [state, setState] = useStateBattle(null);
  const [answer, setAnswer] = useStateBattle('');
  const [flash, setFlash] = useStateBattle('');
  const [hint, setHint] = useStateBattle('');

  const loadLobby = useCallbackBattle(() => {
    api('/api/battle/lobby').then((d) => {
      setLobby(d.battles || []);
      if (d.active_battle_id && !battleId) setBattleId(d.active_battle_id);
    }).catch(() => setLobby([]));
  }, [battleId]);

  useEffectBattle(() => {
    loadLobby();
    const iv = setInterval(() => {
      if (battleId) api(`/api/battle/${battleId}/state`).then(setState).catch(() => {});
      else loadLobby();
    }, 1000);
    return () => clearInterval(iv);
  }, [battleId, loadLobby]);

  // fresh round → clear my typed answer + hint
  const roundNo = state && state.round_no;
  useEffectBattle(() => { setHint(''); setAnswer(''); setFlash(''); }, [roundNo]);

  const timeLeft = useLocalCountdown(state && state.status === 'active' ? state.time_left_ms : 0);
  const revealMs = useLocalCountdown(state && state.status === 'active' ? state.reveal_in_ms : 0);

  const create = async (gameType) => {
    try {
      const d = await api('/api/battle/create', { method: 'POST', body: { game_type: gameType } });
      setSpectating(false);
      setBattleId(d.battle_id);
    } catch (e) { showToast(e.message); }
  };

  const join = async (id) => {
    try {
      await api('/api/battle/join', { method: 'POST', body: { battle_id: id } });
      setSpectating(false);
      setBattleId(id);
    } catch (e) { showToast(e.message); loadLobby(); }
  };

  const watch = (id) => { setSpectating(true); setBattleId(id); };

  const submit = async () => {
    const text = answer.trim();
    if (!text) return;
    try {
      const d = await api('/api/battle/answer', { method: 'POST', body: { battle_id: battleId, text } });
      setAnswer('');
      if (!d.correct) {
        setHint(d.hint || '');
        setFlash('❌ Not it — here comes a hint!');
        setTimeout(() => setFlash(''), 900);
      } else {
        setHint('');
        setFlash('✅ Point for your team!');
        setTimeout(() => setFlash(''), 900);
      }
    } catch (e) { showToast(e.message); }
  };

  const leave = async () => {
    if (!spectating && state && state.status === 'waiting') {
      await api('/api/battle/leave', { method: 'POST', body: { battle_id: battleId } }).catch(() => {});
    }
    setBattleId(null); setState(null); setAnswer(''); setHint(''); setSpectating(false);
    loadLobby();
  };

  if (battleId && state) {
    const isPlayer = !!state.my_team;
    const left = state.team_left || { name: '', score: 0, players: [] };
    const right = state.team_right || { name: '', score: 0, players: [] };
    const scoreline = `${left.name} ${left.score} — ${right.score} ${right.name}`;

    if (state.status === 'waiting') {
      if (isPlayer || state.is_host) {
        return <TeamLobbyView state={state} battleId={battleId} showToast={showToast} onLeave={leave} />;
      }
      // outsider peeking at a waiting lobby — offer join via lobby list
    }

    if (state.status === 'active') {
      if (state.game_type === 'tetris') {
        return (
          <div className="quiz-wrap">
            <div className="tagline">🧱 {scoreline}{!isPlayer && ' · watching'}</div>
            {revealMs > 0 || (isPlayer && state.reveal_in_ms > 0) ? (
              <div className="card battle-card"><CountdownOverlay ms={revealMs} /></div>
            ) : isPlayer ? (
              <TetrisGame battleId={battleId} state={state} showToast={showToast} />
            ) : (
              <div className="card battle-card">
                <div className="battle-title">🧱 Team Tetris in progress</div>
                <div className="battle-sub">{left.name}: {left.score} lines · {right.name}: {right.score} lines</div>
                <button className="duo-btn outline" onClick={leave}>Leave</button>
              </div>
            )}
          </div>
        );
      }

      const counting = revealMs > 0;
      const isFirstRound = state.round_no <= 1;
      const secLeft = Math.ceil(timeLeft / 1000);
      const pct = state.duration_seconds ? Math.min(100, (timeLeft / (state.duration_seconds * 1000)) * 100) : 0;
      return (
        <div className="quiz-wrap">
          <div className="tagline">{GAME_ICON[state.game_type]} {scoreline}{!isPlayer && ' · watching'}</div>
          <div className="card battle-card">
            <div className="battle-timer">
              <div className="battle-timer-bar"><div className="battle-timer-fill" style={{ width: pct + '%' }} /></div>
              <div className={`battle-timer-num ${secLeft <= 10 ? 'urgent' : ''}`}>⏱ {secLeft}s</div>
            </div>
            {counting && isFirstRound ? (
              <CountdownOverlay ms={revealMs} />
            ) : counting ? (
              <>
                {state.last_round_winner && (
                  <div className="battle-lastround">✅ <b>{state.last_round_winner}</b> took "{state.last_round_word}"</div>
                )}
                <div className="battle-countdown small">next word…</div>
              </>
            ) : (
              <>
                <div className="battle-korean">{state.korean_prompt}</div>
                {(hint || state.my_hint) && <div className="battle-hint">💡 {hint || state.my_hint}</div>}
                {isPlayer ? (
                  <>
                    <input
                      className="battle-input"
                      value={answer}
                      onChange={(e) => setAnswer(e.target.value)}
                      onKeyDown={(e) => { if (e.key === 'Enter') submit(); }}
                      placeholder="Type the English + Enter"
                      autoFocus
                    />
                    {flash && <div className="battle-flash">{flash}</div>}
                  </>
                ) : (
                  <div className="battle-sub">Spectating — teams are racing to type this in English</div>
                )}
              </>
            )}
            {!isPlayer && <button className="duo-btn outline" onClick={leave}>Leave</button>}
          </div>
        </div>
      );
    }

    if (state.status === 'finished') {
      const draw = state.winner_team === 'draw';
      return (
        <div className="quiz-wrap">
          {state.winner_is_me && <ConfettiBurst />}
          <div className="tagline">{GAME_ICON[state.game_type]} Battle finished</div>
          <div className="card battle-card">
            <div className="quiz-complete-emoji">{draw ? '🤝' : state.winner_is_me ? '🏆' : isPlayer ? '💀' : '🏁'}</div>
            <div className="battle-title">
              {draw ? "It's a draw!" : state.winner_is_me ? `${state.winner_team_name} wins — that's you!` : `${state.winner_team_name} wins!`}
            </div>
            <div className="battle-sub">{scoreline}</div>
            <div className="quiz-complete-actions">
              {isPlayer && <button className="duo-btn" onClick={() => { const gt = state.game_type; setBattleId(null); setState(null); create(gt); }}>Rematch</button>}
              <button className="duo-btn outline" onClick={leave}>Back to Lobby</button>
            </div>
          </div>
        </div>
      );
    }

    return (
      <div className="quiz-wrap">
        <div className="state-msg">Battle ended.</div>
        <button className="duo-btn outline" onClick={leave}>Back to Lobby</button>
      </div>
    );
  }

  // lobby
  return (
    <div className="quiz-wrap">
      <div className="tagline">⚔️ Battle Lobby</div>
      <div className="track-grid battle-games">
        {BATTLE_GAMES.map((g) => (
          <button key={g.key} className="track-card" onClick={() => create(g.key)}>
            <div className="track-card-icon">{g.icon}</div>
            <div className="track-card-title">{g.title}</div>
            <div className="track-card-desc">{g.desc}</div>
          </button>
        ))}
      </div>
      {lobby && lobby.length > 0 && (
        <div className="card" style={{ width: '100%' }}>
          <div className="mini-lb-title">Live battles</div>
          {lobby.map((b) => (
            <div key={b.id} className="battle-lobby-row">
              <span>{GAME_ICON[b.game_type]}</span>
              <span className="battle-lobby-host">
                {b.host_name}'s match · {b.player_count} player{b.player_count !== 1 ? 's' : ''}
              </span>
              <span className="battle-lobby-time">{b.created_at}</span>
              {b.status === 'waiting' && !b.is_mine && <button className="duo-btn blue small" onClick={() => join(b.id)}>Join</button>}
              {b.status === 'waiting' && b.is_mine && <span className="battle-lobby-mine">yours</span>}
              {b.status === 'active' && <button className="duo-btn outline small" onClick={() => watch(b.id)}>Watch</button>}
            </div>
          ))}
        </div>
      )}
      <button className="duo-btn outline" onClick={onBack}>‹ Back</button>
    </div>
  );
}
