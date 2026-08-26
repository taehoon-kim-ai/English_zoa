// ── app shell — routing + shared me state + toast. Section views (MainView,
// QuizView, ProfileView, TopBar) live in their own files and are loaded
// before this one (see index.html). Profile has no nav tab — it's only
// reached via the avatar button in TopBar (see topbar.jsx).
const { useState: useStateApp, useEffect: useEffectApp, useCallback: useCallbackApp, useRef: useRefApp } = React;

function useHashRoute() {
  const [route, setRoute] = useStateApp(() => window.location.hash.replace('#', '') || 'main');
  useEffectApp(() => {
    const onHashChange = () => setRoute(window.location.hash.replace('#', '') || 'main');
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, []);
  return route;
}

function useToast() {
  const [msg, setMsg] = useStateApp('');
  const timer = useRefApp(null);
  const show = useCallbackApp((text) => {
    setMsg(text);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setMsg(''), 1800);
  }, []);
  const node = <div className={`toast ${msg ? 'show' : ''}`}>{msg}</div>;
  return [show, node];
}

// ── notification center — global 4s poll for battle invites (any screen).
// An invite renders as a card with Yes/No; accepting jumps to the battle
// (QuizView listens for 'phraseup-open-battle'), declining notifies the
// inviter. Responses to MY invites arrive as auto-dismissing toasts.
function NotificationCenter({ showToast }) {
  const [invites, setInvites] = useStateApp([]);
  const [flash, setFlash] = useStateApp([]); // response toasts
  const flashSeq = useRefApp(0);

  useEffectApp(() => {
    const poll = () => {
      api('/api/notifications').then((d) => {
        setInvites(d.invites || []);
        (d.responses || []).forEach((r) => {
          const id = ++flashSeq.current;
          setFlash((f) => [...f, { id, ...r }]);
          setTimeout(() => setFlash((f) => f.filter((x) => x.id !== id)), 6000);
        });
      }).catch(() => {});
    };
    poll();
    const iv = setInterval(poll, 4000);
    return () => clearInterval(iv);
  }, []);

  const respond = async (inv, accept) => {
    setInvites((list) => list.filter((i) => i.invite_id !== inv.invite_id));
    try {
      await api('/api/battle/invite/respond', { method: 'POST', body: { invite_id: inv.invite_id, accept } });
      if (accept) {
        window.__phraseupOpenBattle = true;
        window.location.hash = '#quiz';
        window.dispatchEvent(new CustomEvent('phraseup-open-battle'));
      }
    } catch (e) { showToast(e.message); }
  };

  const gameName = { word: 'Word Race', phrase: 'Phrase Race', tetris: 'Word Tetris' };

  if (invites.length === 0 && flash.length === 0) return null;
  return (
    <div className="noti-stack">
      {invites.map((inv) => (
        <div key={inv.invite_id} className="noti-card">
          {inv.from_avatar
            ? <img className="noti-avatar" src={inv.from_avatar} alt="" />
            : <span className="noti-avatar fallback">{(inv.from_name || '?').charAt(0).toUpperCase()}</span>}
          <div className="noti-body">
            <b>{inv.from_name}</b> invited you to <b>{gameName[inv.game_type] || inv.game_type}</b>!
            <div className="noti-sub">Join the match?</div>
          </div>
          <div className="noti-actions">
            <button className="duo-btn small" onClick={() => respond(inv, true)}>Yes, join</button>
            <button className="duo-btn outline small" onClick={() => respond(inv, false)}>No thanks</button>
          </div>
        </div>
      ))}
      {flash.map((r) => (
        <div key={r.id} className={`noti-card response ${r.accepted ? 'ok' : 'no'}`}>
          <span className="noti-avatar fallback">{r.accepted ? '🎉' : '😢'}</span>
          <div className="noti-body">
            <b>{r.to_name}</b> {r.accepted ? 'joined your' : 'declined your'} <b>{gameName[r.game_type] || r.game_type}</b> invite
          </div>
        </div>
      ))}
    </div>
  );
}

function App() {
  const route = useHashRoute();
  const [me, setMe] = useStateApp(null);
  const [showToast, toastNode] = useToast();

  const loadMe = useCallbackApp(() => {
    api('/api/me').then(setMe).catch(() => {});
  }, []);

  useEffectApp(() => { loadMe(); }, [loadMe]);

  const onCorrectCountChange = (correct_count) => setMe((prev) => (prev ? { ...prev, correct_count } : prev));

  const knownRoutes = ['main', 'quiz', 'translate', 'profile'];
  const activeRoute = knownRoutes.includes(route) ? route : 'main';

  let view;
  if (activeRoute === 'quiz') view = <QuizView me={me} onCorrectCountChange={onCorrectCountChange} showToast={showToast} />;
  else if (activeRoute === 'translate') view = <TranslateView />;
  else if (activeRoute === 'profile') view = <ProfileView me={me} showToast={showToast} />;
  else view = <MainView me={me} />;

  const isWide = activeRoute === 'main' || activeRoute === 'quiz' || activeRoute === 'translate';

  return (
    <>
      <TopBar route={activeRoute} me={me} />
      <main className={isWide ? 'main-wide' : ''}>{view}</main>
      <NotificationCenter showToast={showToast} />
      {toastNode}
    </>
  );
}
