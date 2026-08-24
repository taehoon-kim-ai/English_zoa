// ── app shell — routing + shared me/score state + toast. Section views
// (HomeView, QuizView, ProfileView, LeaderboardView, TopBar) live in their
// own files and are loaded before this one (see index.html).
const { useState: useStateApp, useEffect: useEffectApp, useCallback: useCallbackApp, useRef: useRefApp } = React;

function useHashRoute() {
  const [route, setRoute] = useStateApp(() => window.location.hash.replace('#', '') || 'home');
  useEffectApp(() => {
    const onHashChange = () => setRoute(window.location.hash.replace('#', '') || 'home');
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

  const onScoreChange = (score) => setMe((prev) => (prev ? { ...prev, score } : prev));

  const knownRoutes = ['home', 'quiz', 'profile', 'leaderboard'];
  const activeRoute = knownRoutes.includes(route) ? route : 'home';

  let view;
  if (activeRoute === 'quiz') view = <QuizView onScoreChange={onScoreChange} showToast={showToast} />;
  else if (activeRoute === 'profile') view = <ProfileView me={me} showToast={showToast} />;
  else if (activeRoute === 'leaderboard') view = <LeaderboardView me={me} />;
  else view = <HomeView me={me} onScoreChange={onScoreChange} showToast={showToast} />;

  return (
    <>
      <TopBar route={activeRoute} me={me} />
      <main>{view}</main>
      {toastNode}
    </>
  );
}
