// ── section: leaderboard — pairs with score.go. Two independent boards:
// most correct answers (all-time) and best streak this month. A wrong
// answer never subtracts from either.
const { useState: useStateLB, useEffect: useEffectLB } = React;

function medal(i) {
  return i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : i + 1;
}
function rankClass(i) {
  return i === 0 ? 'top1' : i === 1 ? 'top2' : i === 2 ? 'top3' : '';
}

function LeaderboardTable({ title, icon, rows, me, valueLabel, valueKey, emptyMessage }) {
  return (
    <div className="card">
      <div className="tagline">{icon} {title}</div>
      <table className="leaderboard-table">
        <thead>
          <tr><th>Rank</th><th>Nickname</th><th>{valueLabel}</th></tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.email} className={me && r.email === me.email ? 'me' : ''}>
              <td><span className={`rank-badge ${rankClass(i)}`}>{medal(i)}</span></td>
              <td>{r.nickname}</td>
              <td>{r[valueKey]}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {rows.length === 0 && <div className="state-msg">{emptyMessage}</div>}
    </div>
  );
}

function LeaderboardView({ me }) {
  const [data, setData] = useStateLB(null);
  const [error, setError] = useStateLB('');

  useEffectLB(() => {
    api('/api/leaderboard')
      .then(setData)
      .catch((e) => setError(e.message));
  }, []);

  if (error) return <div className="state-msg">{error}</div>;
  if (!data) return <div className="state-msg">Loading...</div>;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <LeaderboardTable
        title="Most Correct Answers"
        icon="🎯"
        rows={data.quiz || []}
        me={me}
        valueLabel="Correct"
        valueKey="correct_count"
        emptyMessage="No one has answered a question yet — take today's quiz first!"
      />
      <LeaderboardTable
        title="Longest Streak This Month"
        icon="🔥"
        rows={data.streak || []}
        me={me}
        valueLabel="Streak"
        valueKey="best_streak"
        emptyMessage="No streaks logged this month yet."
      />
    </div>
  );
}
