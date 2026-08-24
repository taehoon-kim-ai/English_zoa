// ── section: 리더보드 — pairs with score.go ──────────────────────────────────
const { useState: useStateLB, useEffect: useEffectLB } = React;

function LeaderboardView({ me }) {
  const [rows, setRows] = useStateLB(null);
  const [error, setError] = useStateLB('');

  useEffectLB(() => {
    api('/api/leaderboard')
      .then((data) => setRows(data.leaderboard || []))
      .catch((e) => setError(e.message));
  }, []);

  if (error) return <div className="state-msg">{error}</div>;
  if (!rows) return <div className="state-msg">불러오는 중...</div>;

  const medal = (i) => (i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : i + 1);
  const rankClass = (i) => (i === 0 ? 'top1' : i === 1 ? 'top2' : i === 2 ? 'top3' : '');

  return (
    <div className="card">
      <div className="tagline">이번 주 경쟁 순위</div>
      <table className="leaderboard-table">
        <thead>
          <tr><th>순위</th><th>닉네임</th><th>💎 점수</th></tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.email} className={me && r.email === me.email ? 'me' : ''}>
              <td><span className={`rank-badge ${rankClass(i)}`}>{medal(i)}</span></td>
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
