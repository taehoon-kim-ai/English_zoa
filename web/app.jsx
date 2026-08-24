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
      {toastNode}
    </>
  );
}
