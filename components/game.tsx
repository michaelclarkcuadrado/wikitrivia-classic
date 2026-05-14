import React, { useState } from "react";
import { GameState } from "../types/game";
import { Item } from "../types/item";
import createState from "../lib/create-state";
import Board from "./board";
import Loading from "./loading";
import Instructions from "./instructions";

type PackedItems = {
  v: number;
  dicts: {
    date_prop_id: string[];
    instance_of: string[];
    occupations: string[];
  };
  rows: Array<
    [
      number, // id (Q-prefix stripped)
      string, // label
      number, // year
      string, // description
      string, // image
      string, // wikipedia_title ("" when same as label)
      number, // date_prop_id dict index
      number[], // instance_of dict indices
      number[] | null // occupations dict indices, null for non-humans
    ]
  >;
};

function decodeItems(packed: PackedItems): Item[] {
  const dp = packed.dicts.date_prop_id;
  const io = packed.dicts.instance_of;
  const oc = packed.dicts.occupations;
  return packed.rows.map((r) => ({
    id: "Q" + r[0],
    label: r[1],
    year: r[2],
    description: r[3],
    image: r[4],
    wikipedia_title: r[5] === "" ? r[1] : r[5],
    date_prop_id: dp[r[6]],
    instance_of: r[7].map((i) => io[i]),
    occupations: r[8] === null ? null : r[8].map((i) => oc[i]),
  }));
}

export default function Game() {
  const [state, setState] = useState<GameState | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [started, setStarted] = useState(false);
  const [items, setItems] = useState<Item[] | null>(null);

  React.useEffect(() => {
    const fetchGameData = async () => {
      const res = await fetch("/items.json.gz");
      if (!res.ok || !res.body) {
        throw new Error(`Failed to fetch items.json.gz: ${res.status}`);
      }
      const stream = res.body.pipeThrough(new DecompressionStream("gzip"));
      const text = await new Response(stream).text();
      setItems(decodeItems(JSON.parse(text) as PackedItems));
    };

    fetchGameData();
  }, []);

  React.useEffect(() => {
    (async () => {
      if (items !== null) {
        setState(await createState(items));
        setLoaded(true);
      }
    })();
  }, [items]);

  const resetGame = React.useCallback(() => {
    (async () => {
      if (items !== null) {
        setState(await createState(items));
      }
    })();
  }, [items]);

  const setBoardState = React.useCallback<
    React.Dispatch<React.SetStateAction<GameState>>
  >((update) => {
    setState((prev) => {
      if (prev === null) {
        return prev;
      }
      return typeof update === "function" ? update(prev) : update;
    });
  }, []);

  const [highscore, setHighscore] = React.useState<number>(
    Number(localStorage.getItem("highscore") ?? "0")
  );

  const updateHighscore = React.useCallback((score: number) => {
    localStorage.setItem("highscore", String(score));
    setHighscore(score);
  }, []);

  if (!loaded || state === null) {
    return <Loading />;
  }

  if (!started) {
    return (
      <Instructions highscore={highscore} start={() => setStarted(true)} />
    );
  }

  return (
    <Board
      highscore={highscore}
      state={state}
      setState={setBoardState}
      resetGame={resetGame}
      updateHighscore={updateHighscore}
    />
  );
}
