// ── section: profile (nickname/status/streak calendar) — pairs with profile.go.
// Reached only via the avatar button in TopBar (no nav tab).
const { useState: useStateProfile, useEffect: useEffectProfile } = React;

const WEEKDAY_LABELS = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];

function StreakHero({ streak }) {
  const flameCount = Math.min(streak, 5);
  return (
    <div className="streak-hero">
      <div className="streak-hero-flames">
        {Array.from({ length: Math.max(flameCount, 1) }).map((_, i) => (
          <span key={i} className={`streak-hero-flame ${streak === 0 ? 'dim' : ''}`} style={{ animationDelay: `${i * 0.15}s` }}>🔥</span>
        ))}
      </div>
      <div className="streak-hero-count">{streak}</div>
      <div className="streak-hero-label">{streak === 1 ? 'day streak' : 'day streak'}</div>
    </div>
  );
}

function MonthCalendar({ eventByDate }) {
  const today = new Date();
  const year = today.getFullYear();
  const month = today.getMonth();
  const firstDay = new Date(year, month, 1);
  const daysInMonth = new Date(year, month + 1, 0).getDate();
  const startWeekday = firstDay.getDay();
  const todayStr = today.toISOString().slice(0, 10);

  const cells = [];
  for (let i = 0; i < startWeekday; i++) cells.push(null);
  for (let day = 1; day <= daysInMonth; day++) cells.push(day);

  return (
    <div>
      <div className="month-label">{today.toLocaleString('en-US', { month: 'long', year: 'numeric' })}</div>
      <div className="calendar-weekdays">
        {WEEKDAY_LABELS.map((w, i) => <div key={i} className="weekday-label">{w}</div>)}
      </div>
      <div className="calendar-grid">
        {cells.map((day, i) => {
          if (day === null) return <div key={`blank-${i}`} className="calendar-cell blank" />;
          const dateStr = `${year}-${String(month + 1).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
          const event = eventByDate[dateStr];
          const isToday = dateStr === todayStr;
          return (
            <div
              key={dateStr}
              className={`calendar-cell ${event ? 'logged-in' : ''} ${isToday ? 'today' : ''}`}
              title={event ? `${dateStr} — first login at ${event.time}` : dateStr}
            >
              <span className="day-num">{day}</span>
              {event && <span className="day-flame">🔥</span>}
            </div>
          );
        })}
      </div>
      <div className="calendar-legend">
        <span className="calendar-cell logged-in" style={{ width: 14, height: 14, aspectRatio: 'auto', display: 'inline-block' }} />
        Logged in that day (hover for the time)
      </div>
    </div>
  );
}

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
        <StreakHero streak={me ? me.streak : 0} />
        {loaded && <MonthCalendar eventByDate={eventByDate} />}
      </div>
    </div>
  );
}
