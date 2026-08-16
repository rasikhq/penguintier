import { useEffect, useState } from "react";
import "./App.css";
import { ZeroTierUtils } from "./utils/zerotier";

function App() {
  const [ztAuth, setZTAuth] = useState<string>();

  function initZT() {
    console.log("Initializing...");
    ZeroTierUtils.getAuthToken().then((token) => {
      setZTAuth(token);
    });
  }

  useEffect(() => {
    initZT();
  }, []);

  return (
    <main className="container">
      {!ztAuth && <h1>Initializing...</h1>}
      {ztAuth && (
        <>
          <MyDetails ztAuth={ztAuth} />
          <button
            onClick={() => {
              initZT();
            }}
          >
            Reload
          </button>
        </>
      )}
    </main>
  );
}

function MyDetails({ ztAuth }: { ztAuth: string }) {
  const [networkStatus, setNetworkStatus] = useState();

  useEffect(() => {
    initNetworkStatus();
  }, []);

  function initNetworkStatus() {
    ZeroTierUtils.getNetworkStatus(ztAuth)
      .then((networkStatus) => {
        setNetworkStatus(networkStatus);
      })
      .catch((e) => {
        setNetworkStatus({ ztAuth, e });
      });
  }

  return (
    <>
      <h4>{JSON.stringify({ ztAuth, networkStatus }, null, 4)}</h4>
      <button
        onClick={() => {
          initNetworkStatus();
        }}
      >
        Get Status
      </button>
    </>
  );
}

export default App;
