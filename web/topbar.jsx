// ── section: 상단 네비게이션 — stateless, no hook aliasing needed ────────────
function TopBar({ route, me }) {
  const tabs = [
    { key: 'quiz', label: '퀴즈', icon: '🧠' },
    { key: 'profile', label: '내 페이지', icon: '🙋' },
    { key: 'leaderboard', label: '리더보드', icon: '🏆' },
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
            <span className="stat-pill streak">🔥 {me.streak}</span>
            <span className="stat-pill score">💎 {me.score}</span>
          </>
        )}
      </div>
    </div>
  );
}
