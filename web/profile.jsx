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
  // Local Y-M-D, not toISOString() — toISOString() converts to UTC and would
  // mislabel "today" whenever the local timezone is ahead of UTC (e.g. KST).
  const todayStr = `${year}-${String(month + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`;

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

// Badge shelf — earned badges pop in color, unearned ones sit grayscale as
// goals to chase (badges.go computes them live from quiz/login data).
function BadgeShelf() {
  const [badges, setBadges] = useStateProfile(null);

  useEffectProfile(() => {
    api('/api/stats').then((data) => setBadges(data.badges || [])).catch(() => {});
  }, []);

  if (!badges) return null;

  return (
    <div className="card" style={{ marginTop: 20 }}>
      <div className="tagline" style={{ marginBottom: 12 }}>🏅 Badges</div>
      <div className="badge-grid">
        {badges.map((b) => (
          <div key={b.id} className={`badge-cell ${b.earned ? 'earned' : ''}`} title={b.desc}>
            <div className="badge-icon">{b.icon}</div>
            <div className="badge-name">{b.name}</div>
            <div className="badge-desc">{b.desc}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

function ProfileView({ me, showToast }) {
  const [nickname, setNickname] = useStateProfile('');
  const [statusMessage, setStatusMessage] = useStateProfile('');
  const [goalVocab, setGoalVocab] = useStateProfile(10);
  const [goalPhrase, setGoalPhrase] = useStateProfile(5);
  const [events, setEvents] = useStateProfile([]);
  const [loaded, setLoaded] = useStateProfile(false);
  const [avatarUrl, setAvatarUrl] = useStateProfile('');

  useEffectProfile(() => {
    if (me) {
      setNickname(me.nickname);
      setStatusMessage(me.status_message);
      if (me.goal_vocab) setGoalVocab(me.goal_vocab);
      if (me.goal_phrase) setGoalPhrase(me.goal_phrase);
      if (me.avatar_url) setAvatarUrl(me.avatar_url);
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
      await api('/api/profile', {
        method: 'POST',
        body: {
          nickname,
          status_message: statusMessage,
          goal_vocab: parseInt(goalVocab, 10) || 10,
          goal_phrase: parseInt(goalPhrase, 10) || 5,
        },
      });
      showToast('Saved ✓');
    } catch (e) {
      showToast(e.message);
    }
  };

  const eventByDate = {};
  events.forEach((e) => { eventByDate[e.date] = e; });

  // Resize a chosen photo to a small square data: URL (fits the 200KB cap).
  const pickAvatar = (e) => {
    const file = e.target.files && e.target.files[0];
    if (!file) return;
    const img = new Image();
    img.onload = async () => {
      const size = 128;
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = size;
      const cctx = canvas.getContext('2d');
      const side = Math.min(img.width, img.height);
      cctx.drawImage(img, (img.width - side) / 2, (img.height - side) / 2, side, side, 0, 0, size, size);
      const dataUrl = canvas.toDataURL('image/jpeg', 0.85);
      try {
        await api('/api/profile/avatar', { method: 'POST', body: { avatar: dataUrl } });
        setAvatarUrl(dataUrl);
        showToast('Photo updated ✓');
      } catch (err) { showToast(err.message); }
    };
    img.src = URL.createObjectURL(file);
    e.target.value = '';
  };

  return (
    <div>
      <div className="card">
        <div className="profile-avatar-row">
          {avatarUrl
            ? <img className="profile-avatar" src={avatarUrl} alt="" />
            : <span className="profile-avatar fallback">{(nickname || '?').charAt(0).toUpperCase()}</span>}
          <div className="profile-avatar-meta">
            <div className="profile-avatar-hint">Your photo syncs from Slack (Okta/Google) automatically — or set your own.</div>
            <label className="duo-btn outline small profile-avatar-btn">
              Change photo
              <input type="file" accept="image/*" onChange={pickAvatar} style={{ display: 'none' }} />
            </label>
          </div>
        </div>
        <div className="profile-field">
          <label>Nickname</label>
          <input value={nickname} onChange={(e) => setNickname(e.target.value)} maxLength={24} />
        </div>
        <div className="profile-field">
          <label>Status message</label>
          <input value={statusMessage} onChange={(e) => setStatusMessage(e.target.value)} maxLength={80} />
        </div>
        <div className="goal-fields">
          <div className="profile-field">
            <label>🔤 Daily vocab goal</label>
            <input type="number" min="1" max="100" value={goalVocab} onChange={(e) => setGoalVocab(e.target.value)} />
          </div>
          <div className="profile-field">
            <label>💬 Daily phrase goal</label>
            <input type="number" min="1" max="100" value={goalPhrase} onChange={(e) => setGoalPhrase(e.target.value)} />
          </div>
        </div>
        <button className="duo-btn" onClick={save}>Save</button>
      </div>

      <div className="card" style={{ marginTop: 20 }}>
        <StreakHero streak={me ? me.streak : 0} />
        {loaded && <MonthCalendar eventByDate={eventByDate} />}
      </div>

      <BadgeShelf />
    </div>
  );
}
