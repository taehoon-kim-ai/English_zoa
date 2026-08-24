// ── section: 내 페이지 (닉네임/상태메시지/캘린더) — pairs with profile.go ────
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
      showToast('저장했어요 ✓');
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
          <label>닉네임</label>
          <input value={nickname} onChange={(e) => setNickname(e.target.value)} maxLength={24} />
        </div>
        <div className="profile-field">
          <label>상태 메시지</label>
          <input value={statusMessage} onChange={(e) => setStatusMessage(e.target.value)} maxLength={80} />
        </div>
        <button className="duo-btn" onClick={save}>저장</button>
      </div>

      <div className="card" style={{ marginTop: 20 }}>
        <label style={{ fontSize: 12, fontWeight: 800, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
          최근 6주 접속 기록
        </label>
        {loaded && (
          <div className="calendar-grid">
            {days.map((d) => (
              <div key={d.key} className={`calendar-cell ${d.event ? 'logged-in' : ''}`} title={d.event ? `${d.key} ${d.event.time} 접속` : d.key}>
                <span className="day-num">{d.dayNum}</span>
                {d.event ? `🔥${d.event.time}` : ''}
              </div>
            ))}
          </div>
        )}
        <div className="calendar-legend">
          <span className="calendar-cell logged-in" style={{ width: 14, height: 14, aspectRatio: 'auto', display: 'inline-block' }} />
          접속한 날 (표시된 시각 = 그날 첫 접속 시각)
        </div>
      </div>
    </div>
  );
}
