// ── section: top nav — stateless, no hook aliasing needed ────────────────
function TopBar({ route, me }) {
  const tabs = [
    { key: 'quiz', label: 'Quiz', icon: '🧠' },
    { key: 'profile', label: 'Profile', icon: '🙋' },
    { key: 'leaderboard', label: 'Leaderboard', icon: '🏆' },
  ];
  return (
    <div className="topbar">
      <div className="topbar-brand">English<span>_zoa</span></div>
      <nav className="topbar-nav">
        {tabs.map((t) => (
          <a key={t.key} href={`#${t.key}`} className={route === t.key ? 'active' : ''}>
            <span>{t.icon}</span>{t.label}
          </a>
        ))}
      </nav>
      <div className="topbar-stats">
        {me && (
          <>
            <span className="stat-pill streak" title="Current streak">🔥 {me.streak}</span>
            <span className="stat-pill score" title="Correct answers">🎯 {me.correct_count}</span>
          </>
        )}
      </div>
    </div>
  );
}
