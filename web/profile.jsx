// ── section: profile (nickname/status/streak calendar) — pairs with profile.go
const { useState: useStateProfile, useEffect: useEffectProfile } = React;

function ProfileView({ me, showToast }) {
  const [nickname, setNickname] = useStateProfile('');
  const [statusMessage, setStatusMessage] = useStateProfile('');
  const [events, setEvents] = useStateProfile([]);
  const [loaded, setLoaded] = useStateProfile(false);

  useEffectProfile(() => {
    if (me) {
      setNickname(me.nickname);
      setStatusMessage(me.status_message);
    }
  }, [me]);

  useEffectProfile(() => {
    api('/api/calendar')
      .then((data) => setEvents(data.events || []))
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, []);

  const save = async () => {
    try {
      await api('/api/profile', { method: 'POST', body: { nickname, status_message: statusMessage } });
      showToast('Saved ✓');
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
          <label>Nickname</label>
          <input value={nickname} onChange={(e) => setNickname(e.target.value)} maxLength={24} />
        </div>
        <div className="profile-field">
          <label>Status message</label>
          <input value={statusMessage} onChange={(e) => setStatusMessage(e.target.value)} maxLength={80} />
        </div>
        <button className="duo-btn" onClick={save}>Save</button>
      </div>

      <div className="card" style={{ marginTop: 20 }}>
        <label style={{ fontSize: 12, fontWeight: 800, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
          Last 6 weeks of activity
        </label>
        {loaded && (
          <div className="calendar-grid">
            {days.map((d) => (
              <div key={d.key} className={`calendar-cell ${d.event ? 'logged-in' : ''}`} title={d.event ? `${d.key} — first login at ${d.event.time}` : d.key}>
                <span className="day-num">{d.dayNum}</span>
                {d.event ? `🔥${d.event.time}` : ''}
              </div>
            ))}
          </div>
        )}
        <div className="calendar-legend">
          <span className="calendar-cell logged-in" style={{ width: 14, height: 14, aspectRatio: 'auto', display: 'inline-block' }} />
          Logged in that day (time shown = first login that day)
        </div>
      </div>
    </div>
  );
}
