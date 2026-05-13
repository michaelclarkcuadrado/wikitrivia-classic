export interface Item {
  date_prop_id: string;
  description: string;
  id: string;
  image: string;
  instance_of: string[];
  label: string;
  occupations: string[] | null;
  wikipedia_title: string;
  year: number;
}

export type PlayedItem = Item & {
  played: {
    correct: boolean;
  };
};
