import { create } from "zustand";
import {
  login as apiLogin,
  register as apiRegister,
  getStoredToken,
  clearToken,
} from "../api/auth";
import { setOnUnauthorized } from "../api/client";
import { apiClient } from "../api/client";
import type { User } from "@teamagentica/api-client";

interface AuthStore {
  authenticated: boolean;
  sessionExpired: boolean;
  user: User | null;
  users: User[];
  loading: boolean;
  error: string | null;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string, displayName: string) => Promise<void>;
  logout: () => void;
  dismissSessionExpired: () => void;
  fetchUser: () => Promise<void>;
  fetchUsers: () => Promise<void>;
}

export const useAuthStore = create<AuthStore>((set, get) => {
  // Register global 401 handler. This only fires AFTER the API client
  // has already tried (and failed) to refresh the token silently.
  setOnUnauthorized(() => {
    clearToken();
    set({ authenticated: false, sessionExpired: true, user: null, users: [] });
  });

  return ({
  authenticated: !!getStoredToken(),
  sessionExpired: false,
  user: null,
  users: [],
  loading: false,
  error: null,

  login: async (email, password) => {
    set({ loading: true, error: null, sessionExpired: false });
    try {
      await apiLogin(email, password);
      set({ authenticated: true, loading: false });
      await get().fetchUser();
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Login failed",
      });
      throw err;
    }
  },

  register: async (email, password, displayName) => {
    set({ loading: true, error: null, sessionExpired: false });
    try {
      await apiRegister(email, password, displayName);
      set({ authenticated: true, loading: false });
      await get().fetchUser();
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Registration failed",
      });
      throw err;
    }
  },

  logout: () => {
    apiClient.auth.logout();
    clearToken();
    set({ authenticated: false, sessionExpired: false, user: null, users: [], error: null });
  },

  dismissSessionExpired: () => {
    set({ sessionExpired: false });
  },

  fetchUser: async () => {
    try {
      const user = await apiClient.auth.getMe();
      set({ user });
    } catch {
      clearToken();
      set({ authenticated: false, user: null });
    }
  },

  fetchUsers: async () => {
    try {
      const users = await apiClient.auth.getUsers();
      set({ users });
    } catch {
      // silently fail — non-admin users can't list users
    }
  },
});
});
