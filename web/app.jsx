// ── app shell — routing + shared me/score state + toast. Section views
// (QuizView, ProfileView, LeaderboardView, TopBar) live in their
// own files and are loaded before this one (see index.html).
const { useState: useStateApp, useEffect: useEffectApp, useCallback: useCallbackApp, useRef: useRefApp } = React;

function useHashRoute() {
  const [route, setRoute] = useStateApp(() => window.location.hash.replace('#', '') || 'quiz');
  useEffectApp(() => {
    const onHashChange = () => setRoute(window.location.hash.replace('#', '') || 'quiz');
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

  const knownRoutes = ['quiz', 'profile', 'leaderboard'];
  const activeRoute = knownRoutes.includes(route) ? route : 'quiz';

  let view;
  if (activeRoute === 'profile') view = <ProfileView me={me} showToast={showToast} />;
  else if (activeRoute === 'leaderboard') view = <LeaderboardView me={me} />;
  else view = <QuizView onCorrectCountChange={onCorrectCountChange} showToast={showToast} />;

  return (
    <>
      <TopBar route={activeRoute} me={me} />
      <main>{view}</main>
      {toastNode}
    </>
  );
}
