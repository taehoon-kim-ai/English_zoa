// ── section: word battle — pairs with battle.go. Lobby (create/join/watch),
// best-of-10 typing races (word/phrase), and battle tetris where each piece
// is earned by answering a vocab question and cleared lines send the
// opponent a one-gap garbage line. All sync is 1s polling of battle state.
const { useState: useStateBattle, useEffect: useEffectBattle, useRef: useRefBattle, useCallback: useCallbackBattle } = React;

const BATTLE_GAMES = [
  { key: 'word', icon: '🔤', title: 'Word Race', desc: 'First to type the word · 10 rounds' },
  { key: 'phrase', icon: '💬', title: 'Phrase Race', desc: 'First to type the sentence · 10 rounds' },
  { key: 'tetris', icon: '🧱', title: 'Word Tetris', desc: 'Answer to earn pieces · clears attack' },
];

const GAME_ICON = { word: '🔤', phrase: '💬', tetris: '🧱' };

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
const TET_COLORS = { I: '#1cb0f6', O: '#ffc800', T: '#ce82ff', S: '#58cc02', Z: '#ff4b4b', J: '#1899d6', L: '#ff9600', G: '#9aa0aa' };
const TET_KEYS = Object.keys(TET_SHAPES);

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
  const [linesCleared, setLinesCleared] = useStateBattle(0);
  const consumedRef = useRefBattle(false);
  const modeRef = useRefBattle('gate');
  modeRef.current = mode;

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
      setLinesCleared((n) => n + cleared);
      api('/api/battle/tetris/lines', { method: 'POST', body: { battle_id: battleId, lines: cleared } }).catch(() => {});
    }
    // next piece is gated behind a question
    setMode('gate');
    fetchGate();
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

  // gravity
  useEffectBattle(() => {
    const iv = setInterval(() => { if (modeRef.current === 'playing') move(0, 1); }, 650);
    return () => clearInterval(iv);
  }, []);

  // keyboard
  useEffectBattle(() => {
    const onKey = (e) => {
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
    if (pending > 0 && !consumedRef.current) {
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

  const answerGate = async (english) => {
    try {
      const d = await api('/api/battle/tetris/gate', { method: 'POST', body: { battle_id: battleId, english, sig: gate.sig } });
      if (d.correct) {
        setGate(null);
        setGateFlash('');
        spawn();
      } else {
        setGateFlash('❌ Wrong — new question!');
        setTimeout(() => { setGateFlash(''); fetchGate(); }, 700);
      }
    } catch (e) { showToast(e.message); }
  };

  // compose render grid (board + falling piece)
  const cells = gridRef.current.map((row) => [...row]);
  const p = pieceRef.current;
  if (p) {
    for (let r = 0; r < p.shape.length; r++)
      for (let c = 0; c < p.shape[r].length; c++)
        if (p.shape[r][c] && p.y + r >= 0) cells[p.y + r][p.x + c] = p.key;
  }

  const iAmHost = state.role === 'host';
  const myLines = iAmHost ? state.host_lines : state.guest_lines;
  const oppLines = iAmHost ? state.guest_lines : state.host_lines;
  const oppName = iAmHost ? state.guest_name : state.host_name;

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
        {mode === 'gate' && gate && (
          <div className="tetris-gate">
            <div className="tetris-gate-title">Earn your next piece</div>
            <div className="tetris-gate-korean">{gate.korean}</div>
            <div className="tetris-gate-options">
              {gate.options.map((o) => (
                <button key={o.english} className="quiz-option" onClick={() => answerGate(o.english)}>{o.english}</button>
              ))}
            </div>
            {gateFlash && <div className="battle-flash">{gateFlash}</div>}
          </div>
        )}
        {mode === 'dead' && <div className="tetris-gate"><div className="tetris-gate-title">💀 Topped out</div></div>}
      </div>
      <div className="tetris-side">
        <div className="tetris-stat"><span>You</span><b>{Math.max(myLines, linesCleared)} lines</b></div>
        <div className="tetris-stat"><span>{oppName}</span><b>{oppLines} lines</b></div>
        <div className="tetris-help">← → move · ↑ rotate · ↓ soft drop · Space hard drop</div>
        <div className="tetris-help">Clear a line → they get a garbage line with one gap</div>
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
        setFlash('❌ Not it — keep going!');
        setTimeout(() => setFlash(''), 900);
      } else if (d.won_round && !d.won_match) {
        setFlash('✅ Round yours!');
        setTimeout(() => setFlash(''), 900);
      }
    } catch (e) { showToast(e.message); }
  };

  const leave = async () => {
    if (!spectating) await api('/api/battle/cancel', { method: 'POST', body: {} }).catch(() => {});
    setBattleId(null); setState(null); setAnswer(''); setSpectating(false);
    loadLobby();
  };

  if (battleId && state) {
    const isPlayer = state.role === 'host' || state.role === 'guest';
    const scoreline = `${state.host_name} ${state.host_score} — ${state.guest_score} ${state.guest_name || '?'}`;

    if (state.status === 'waiting' && isPlayer) {
      return (
        <div className="quiz-wrap">
          <div className="tagline">{GAME_ICON[state.game_type]} Waiting for an opponent...</div>
          <div className="card battle-card">
            <div className="battle-waiting-spin">⏳</div>
            <div className="battle-sub">Anyone can join from the battle lobby</div>
            <button className="duo-btn outline" onClick={leave}>Cancel</button>
          </div>
        </div>
      );
    }

    if (state.status === 'active') {
      if (state.game_type === 'tetris') {
        return (
          <div className="quiz-wrap">
            <div className="tagline">🧱 {state.host_name} vs {state.guest_name}{!isPlayer && ' · watching'}</div>
            {isPlayer
              ? <TetrisGame battleId={battleId} state={state} showToast={showToast} />
              : (
                <div className="card battle-card">
                  <div className="battle-title">🧱 Word Tetris in progress</div>
                  <div className="battle-sub">{state.host_name}: {state.host_lines} lines · {state.guest_name}: {state.guest_lines} lines</div>
                  <button className="duo-btn outline" onClick={leave}>Leave</button>
                </div>
              )}
          </div>
        );
      }
      const counting = state.reveal_in_ms > 0;
      return (
        <div className="quiz-wrap">
          <div className="tagline">{GAME_ICON[state.game_type]} {scoreline} · Round {state.round_no}/{state.rounds_total}{!isPlayer && ' · watching'}</div>
          <div className="card battle-card">
            {state.last_round_winner && (
              <div className="battle-lastround">Last round: <b>{state.last_round_winner}</b> took "{state.last_round_word}"</div>
            )}
            {counting ? (
              <div className="battle-countdown">{Math.ceil(state.reveal_in_ms / 1000)}</div>
            ) : (
              <>
                <div className="battle-korean">{state.korean_prompt}</div>
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
                  <div className="battle-sub">Spectating — players are racing to type this in English</div>
                )}
              </>
            )}
            {!isPlayer && <button className="duo-btn outline" onClick={leave}>Leave</button>}
          </div>
        </div>
      );
    }

    if (state.status === 'finished') {
      return (
        <div className="quiz-wrap">
          {state.winner_is_me && <ConfettiBurst />}
          <div className="tagline">{GAME_ICON[state.game_type]} Battle finished</div>
          <div className="card battle-card">
            <div className="quiz-complete-emoji">{state.winner_is_me ? '🏆' : isPlayer ? '💀' : '🏁'}</div>
            <div className="battle-title">{state.winner_is_me ? 'You won!' : `${state.winner_name} wins!`}</div>
            {state.game_type !== 'tetris' && <div className="battle-sub">{scoreline}</div>}
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
                {b.host_name}{b.guest_name ? ` vs ${b.guest_name}` : ''}
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
