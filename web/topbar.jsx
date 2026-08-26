// ── section: top nav — stateless, no hook aliasing needed. Profile has no
// nav tab of its own — it's reached only via the avatar button on the right.
function TopBar({ route, me }) {
  const tabs = [
    { key: 'main', label: 'Main', icon: '🏠' },
    { key: 'quiz', label: 'Quiz', icon: '🧠' },
    { key: 'translate', label: 'Translator', icon: '🌐' },
  ];
  const initial = me && me.nickname ? me.nickname.trim().charAt(0).toUpperCase() : '?';
  return (
    <div className="topbar">
      <a href="#main" className="topbar-brand">Phrase<span>Up</span></a>
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
        <a href="#profile" className={`avatar-btn ${route === 'profile' ? 'active' : ''}`} title="Profile">
          {me && me.avatar_url ? <img src={me.avatar_url} alt="" /> : initial}
        </a>
      </div>
    </div>
  );
}
